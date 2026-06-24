//go:build linux

package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/isellar/hyperios/internal/events"
)

const (
	inotifyEventSize = syscall.SizeofInotifyEvent

	// inotify event masks we care about
	inCreate   = syscall.IN_CREATE
	inModify   = syscall.IN_MODIFY
	inDelete   = syscall.IN_DELETE
	inMovedTo  = syscall.IN_MOVED_TO  // atomic rename target (apt, editors write this way)
	inMovedFrom = syscall.IN_MOVED_FROM
)

// Watcher watches filesystem paths using inotify and updates the manifest
// when changes are detected.
type Watcher struct {
	store     *Store
	notifier  *events.Notifier
	watchPaths []string
	fd        int
	wds       map[int]string // watch descriptor → path
	done      chan struct{}
}

// NewWatcher creates a Watcher for the given paths.
func NewWatcher(store *Store, notifier *events.Notifier, watchPaths []string) (*Watcher, error) {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		store:      store,
		notifier:   notifier,
		watchPaths: watchPaths,
		fd:         fd,
		wds:        make(map[int]string),
		done:       make(chan struct{}),
	}

	mask := uint32(inCreate | inModify | inDelete | inMovedTo | inMovedFrom)

	for _, path := range watchPaths {
		wd, err := syscall.InotifyAddWatch(fd, path, mask)
		if err != nil {
			// Path may not exist yet — skip silently
			continue
		}
		w.wds[wd] = path
	}

	return w, nil
}

// Start begins watching in a background goroutine. Call Stop to terminate.
func (w *Watcher) Start() {
	go w.loop()
}

// Stop signals the watcher to stop and closes the inotify file descriptor.
func (w *Watcher) Stop() {
	close(w.done)
	syscall.Close(w.fd)
}

func (w *Watcher) loop() {
	buf := make([]byte, 4096)

	for {
		select {
		case <-w.done:
			return
		default:
		}

		n, err := syscall.Read(w.fd, buf)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
				// Non-blocking — no events available; sleep briefly
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return // fd closed or error
		}

		for offset := 0; offset < n; {
			if offset+inotifyEventSize > n {
				break
			}

			event := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[offset]))
			watchDir := w.wds[int(event.Wd)]

			nameBytes := buf[offset+inotifyEventSize : offset+inotifyEventSize+int(event.Len)]
			name := strings.TrimRight(string(nameBytes), "\x00")

			changedPath := watchDir
			if name != "" {
				changedPath = filepath.Join(watchDir, name)
			}

			w.handleChange(changedPath)
			offset += inotifyEventSize + int(event.Len)
		}
	}
}

func (w *Watcher) handleChange(path string) {
	// Re-scan the changed path
	if _, err := os.Stat(path); err == nil {
		w.store.ScanPath(path)
	}

	// Publish manifest updated event
	if w.notifier != nil {
		w.notifier.Publish(events.Event{
			Kind:      events.EventManifestUpdated,
			Payload:   path,
			Timestamp: time.Now(),
		})
	}

	// Persist updated manifest
	_ = w.store.Save()
}
