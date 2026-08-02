package memory

import (
	"encoding/json"
	"math"
	"strings"
	"unicode"
)

// This file implements a small in-memory inverted index with BM25-style
// relevance scoring for LongTermMemory.Search. The previous implementation
// required the *entire* query string to appear as a contiguous substring
// somewhere in an entry's key/value/tags, which meant two differently
// worded-but-related pieces of text (e.g. "install nginx" vs "set up an
// nginx reverse proxy") would never match each other. Tokenizing into words
// and scoring by term overlap (weighted by how distinctive each term is,
// via IDF) fixes that while remaining a pure-Go, dependency-free approach —
// no embedding model or vector store required.

// stopwords is a small list of very common English words that carry little
// discriminating power for relevance ranking. Filtering them keeps the
// index smaller and prevents e.g. "the"/"a" from padding scores; it is
// deliberately conservative (nouns/verbs that might matter for a goal
// description are never included here).
var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "for": true, "from": true, "has": true,
	"have": true, "in": true, "is": true, "it": true, "of": true, "on": true,
	"or": true, "that": true, "the": true, "this": true, "to": true,
	"was": true, "were": true, "will": true, "with": true,
}

// tokenize splits s into lowercase word tokens, treating any run of
// non-letter/non-digit runes as a separator, and drops stopwords. Unicode
// letters/digits are honored (not just ASCII) so non-English text tokenizes
// reasonably too.
func tokenize(s string) []string {
	var tokens []string
	var cur strings.Builder

	flush := func() {
		if cur.Len() == 0 {
			return
		}
		tok := cur.String()
		cur.Reset()
		if !stopwords[tok] {
			tokens = append(tokens, tok)
		}
	}

	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

// documentText concatenates everything about an entry that should be
// searchable — key, JSON-serialised value, and tags — into a single string
// for tokenization. Mirrors what the old substring-based matches() function
// checked, just fed through tokenize() instead of a raw Contains check.
func documentText(e *MemoryEntry) string {
	var sb strings.Builder
	sb.WriteString(e.Key)
	sb.WriteByte(' ')
	if vb, err := json.Marshal(e.Value); err == nil {
		sb.Write(vb)
		sb.WriteByte(' ')
	}
	for _, t := range e.Tags {
		sb.WriteString(t)
		sb.WriteByte(' ')
	}
	return sb.String()
}

// bm25Params holds the standard Okapi BM25 tuning constants. k1 controls
// term-frequency saturation (higher = each additional occurrence of a term
// matters more before diminishing returns kick in); b controls how much
// document length is normalized against the average (0 = no normalization,
// 1 = full normalization). 1.2/0.75 are the conventional defaults used by
// most BM25 implementations (e.g. Lucene's) and are a reasonable default
// for the short, natural-language text stored here (goal descriptions,
// outcome summaries) without needing to tune per-deployment.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// invertedIndex maps tokens to the set of entry keys containing them (with
// per-document term frequency), enabling BM25 scoring without rescanning
// every stored entry on every Search call. It is kept in memory only and
// rebuilt from disk on first use (see LongTermMemory.ensureLoadedLocked);
// Store/Forget keep it incrementally in sync afterward.
type invertedIndex struct {
	// postings maps token -> (entry key -> term frequency in that entry).
	postings map[string]map[string]int
	// docTermFreq maps entry key -> (token -> term frequency), the same
	// data as postings but indexed by document, so remove() can undo a
	// document's contribution to postings/docLen without a full rescan.
	docTermFreq map[string]map[string]int
	// docLen maps entry key -> total token count (with repetition) for
	// that entry, used for BM25's document-length normalization term.
	docLen map[string]int
	// totalLen and docCount track the corpus-wide token count and document
	// count, used to compute the average document length for BM25.
	totalLen int
	docCount int
}

func newInvertedIndex() *invertedIndex {
	return &invertedIndex{
		postings:    make(map[string]map[string]int),
		docTermFreq: make(map[string]map[string]int),
		docLen:      make(map[string]int),
	}
}

// add indexes a document (identified by key) with the given tokens. The
// caller must ensure remove(key) was already called if a document under
// this key was previously indexed (see update).
func (idx *invertedIndex) add(key string, tokens []string) {
	if len(tokens) == 0 {
		return
	}
	freq := make(map[string]int, len(tokens))
	for _, t := range tokens {
		freq[t]++
	}
	idx.docTermFreq[key] = freq
	idx.docLen[key] = len(tokens)
	idx.totalLen += len(tokens)
	idx.docCount++
	for t, f := range freq {
		m, ok := idx.postings[t]
		if !ok {
			m = make(map[string]int)
			idx.postings[t] = m
		}
		m[key] = f
	}
}

// remove un-indexes the document under key, if present. Safe to call on a
// key that was never indexed (no-op).
func (idx *invertedIndex) remove(key string) {
	freq, ok := idx.docTermFreq[key]
	if !ok {
		return
	}
	for t := range freq {
		if m, ok := idx.postings[t]; ok {
			delete(m, key)
			if len(m) == 0 {
				delete(idx.postings, t)
			}
		}
	}
	idx.totalLen -= idx.docLen[key]
	idx.docCount--
	delete(idx.docLen, key)
	delete(idx.docTermFreq, key)
}

// update replaces whatever was indexed under key with tokens (equivalent
// to remove followed by add), used by Store to keep the index consistent
// on overwrite of an existing entry.
func (idx *invertedIndex) update(key string, tokens []string) {
	idx.remove(key)
	idx.add(key, tokens)
}

// avgDocLen returns the mean document length across the corpus, used in
// BM25's length-normalization term. Returns 1 (a safe non-zero default)
// when the corpus is empty or all documents are empty, to avoid a
// divide-by-zero in score().
func (idx *invertedIndex) avgDocLen() float64 {
	if idx.docCount == 0 {
		return 1
	}
	avg := float64(idx.totalLen) / float64(idx.docCount)
	if avg == 0 {
		return 1
	}
	return avg
}

// scoredDoc is one scored search result before entries are resolved.
type scoredDoc struct {
	key   string
	score float64
}

// score computes a BM25 relevance score for every indexed document that
// contains at least one of queryTokens, returning them unsorted (callers
// sort by score). Query tokens are deduplicated first so a repeated word in
// the query doesn't distort scoring.
//
// Uses the BM25+ variant of IDF (log((N-n+0.5)/(n+0.5) + 1)) rather than
// the classic Robertson-Sparck-Jones formula, which guarantees a strictly
// positive IDF for every term regardless of how common it is in the corpus
// (the classic formula can go negative for terms appearing in more than
// half the documents, which would otherwise let very common terms actively
// penalize a document's score).
func (idx *invertedIndex) score(queryTokens []string) []scoredDoc {
	if idx.docCount == 0 {
		return nil
	}

	uniq := make(map[string]bool, len(queryTokens))
	for _, t := range queryTokens {
		uniq[t] = true
	}

	avgdl := idx.avgDocLen()
	n := float64(idx.docCount)

	acc := make(map[string]float64)
	for term := range uniq {
		docs, ok := idx.postings[term]
		if !ok {
			continue
		}
		df := float64(len(docs))
		idf := math.Log((n-df+0.5)/(df+0.5) + 1)
		for key, tf := range docs {
			dl := float64(idx.docLen[key])
			denom := float64(tf) + bm25K1*(1-bm25B+bm25B*(dl/avgdl))
			acc[key] += idf * (float64(tf) * (bm25K1 + 1)) / denom
		}
	}

	results := make([]scoredDoc, 0, len(acc))
	for key, s := range acc {
		results = append(results, scoredDoc{key: key, score: s})
	}
	return results
}
