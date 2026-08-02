package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/isellar/hyperios/internal/config"
	"github.com/isellar/hyperios/internal/llm"
	"github.com/isellar/hyperios/internal/localmodel"
)

// buildLLMClient assembles the Completer used across all modules, based on
// cfg.LocalModelEnabled:
//
//   - If local model use is enabled (only ever set via a confirmed
//     'hyperi models setup'/'enable'), returns a HybridCompleter that tries
//     the local Ollama model first and falls back to the configured remote
//     provider on any failure (Ollama not running, model not pulled, tool-use
//     unsupported by the model, etc).
//   - Otherwise, returns the remote provider client directly — identical to
//     pre-local-model behavior.
func buildLLMClient(cfg *config.Config) llm.Completer {
	apiKey := resolveAPIKey(cfg)
	remote := llm.NewClientForProvider(cfg.LLMProvider, apiKey, cfg.LLMModel)

	if !cfg.LocalModelEnabled || cfg.LocalModelName == "" {
		return remote
	}

	numCtx := cfg.LocalModelNumCtx
	if numCtx == 0 {
		// "Auto" — recompute from currently-detected hardware rather than
		// trusting a value baked in at setup time (relevant if the user
		// added/removed a GPU since running 'hyperi models setup').
		hw := localmodel.DetectHardware()
		if spec, ok := findCatalogSpec(cfg.LocalModelName); ok {
			numCtx = localmodel.RecommendNumCtx(spec, hw.VRAMTotalMB)
		} else {
			numCtx = localmodel.MinRecommendedNumCtx
		}
	}

	local := llm.NewOllamaClientWithOptions(cfg.OllamaBaseURL, cfg.LocalModelName, llm.OllamaOptions{
		NumCtx:    numCtx,
		KeepAlive: cfg.LocalModelKeepAlive,
	})
	return llm.NewHybridCompleter(local, remote, func(err error) {
		fmt.Fprintf(os.Stderr, "[local model] falling back to remote provider: %v\n", err)
	})
}

// findCatalogSpec looks up name in localmodel.Catalog, for models that match
// one of the curated options exactly. Custom/override model names (see
// 'hyperi models setup --model') won't be found here, since we have no VRAM
// profile for them — callers fall back to localmodel.MinRecommendedNumCtx.
func findCatalogSpec(name string) (localmodel.ModelSpec, bool) {
	for _, spec := range localmodel.Catalog {
		if spec.Name == name {
			return spec, true
		}
	}
	return localmodel.ModelSpec{}, false
}

// ── models command ────────────────────────────────────────────────────────────

func buildModelsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Manage the local (Ollama) model used to avoid paid API calls",
		Long: `HyperiOS can run a local model via Ollama for most goal execution,
falling back to your configured remote provider (Anthropic/OpenCode Zen) only
when the local model fails or can't handle a request. This avoids per-call
billing for the common case.

Nothing about your setup changes until you run 'hyperi models setup' and
confirm — no models are downloaded and no config is modified automatically.`,
	}
	cmd.AddCommand(buildModelsDetectCmd())
	cmd.AddCommand(buildModelsSetupCmd())
	cmd.AddCommand(buildModelsStatusCmd())
	cmd.AddCommand(buildModelsEnableCmd())
	cmd.AddCommand(buildModelsDisableCmd())
	cmd.AddCommand(buildModelsRemoveCmd())
	return cmd
}

func buildModelsDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Show detected hardware and the model that would be picked",
		RunE: func(cmd *cobra.Command, args []string) error {
			hw := localmodel.DetectHardware()
			printHardware(hw)

			spec, ok := localmodel.PickModel(hw)
			fmt.Println()
			if !ok {
				fmt.Println("No local model in the curated list fits this hardware.")
				fmt.Println("You can still enable local mode manually with a smaller/custom model:")
				fmt.Println("  hyperi config set local_model_name <ollama-model-tag>")
				return nil
			}
			fmt.Printf("Recommended model: %s (%.1fGB download)\n", spec.Name, spec.DiskGB)
			fmt.Printf("  %s\n", spec.Description)
			fmt.Println()
			fmt.Println("Run 'hyperi models setup' to confirm and pull this model.")
			return nil
		},
	}
}

func printHardware(hw localmodel.Hardware) {
	fmt.Println("Detected hardware:")
	if hw.HasGPU() {
		fmt.Printf("  GPU:  %s (%d MB VRAM total, %d MB free)\n", hw.GPUName, hw.VRAMTotalMB, hw.VRAMFreeMB)
	} else {
		fmt.Println("  GPU:  none detected (CPU-only)")
	}
	fmt.Printf("  RAM:  %d MB total, %d MB available\n", hw.SystemRAMTotalMB, hw.SystemRAMAvailableMB)
	fmt.Printf("  CPU:  %d logical cores\n", hw.CPUCores)
}

func buildModelsSetupCmd() *cobra.Command {
	var (
		yes       bool
		modelName string
		baseURL   string
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Detect hardware, pick a model, confirm, pull it, and enable local mode",
		Long: `Detects local GPU/RAM, picks the best-fitting model from a small curated
list of Qwen2.5-Instruct sizes (all verified to support Ollama tool-calling),
and asks for confirmation before downloading anything or changing config.

After confirming, the model is pulled via the Ollama daemon (which must
already be installed and running — see https://ollama.com/download) and
local_model_enabled is set to true in config. Every goal will then try the
local model first and fall back to your remote provider on failure.

Use --yes to skip the confirmation prompt (e.g. for scripted provisioning).
Use --model to override the auto-picked model tag.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModelsSetup(modelName, baseURL, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	cmd.Flags().StringVar(&modelName, "model", "", "Override the auto-picked model tag (e.g. qwen2.5:14b)")
	cmd.Flags().StringVar(&baseURL, "ollama-url", "", "Ollama daemon base URL (default: http://localhost:11434)")
	return cmd
}

func runModelsSetup(modelOverride, baseURLOverride string, skipConfirm bool) error {
	cfg, err := loadConfig("")
	if err != nil {
		return err
	}

	baseURL := baseURLOverride
	if baseURL == "" {
		baseURL = cfg.OllamaBaseURL
	}
	if baseURL == "" {
		baseURL = localmodel.DefaultBaseURL
	}

	ctx := context.Background()
	mgr := localmodel.NewManager(baseURL)
	if !mgr.Available(ctx) {
		return fmt.Errorf(
			"ollama daemon not reachable at %s\n\n"+
				"Install Ollama first: https://ollama.com/download\n"+
				"Then make sure it's running (usually a systemd service, or 'ollama serve')",
			baseURL,
		)
	}

	hw := localmodel.DetectHardware()
	printHardware(hw)
	fmt.Println()

	var spec localmodel.ModelSpec
	if modelOverride != "" {
		// If the override matches a curated catalog entry (e.g. the user
		// wants to force a specific size rather than accept the auto-pick),
		// reuse its VRAM/context profile. Only truly custom/unknown model
		// names fall back to a bare ModelSpec with no sizing profile.
		if known, ok := findCatalogSpec(modelOverride); ok {
			spec = known
		} else {
			spec = localmodel.ModelSpec{Name: modelOverride, Description: "user-specified override (no sizing profile — using conservative context window)"}
		}
	} else {
		picked, ok := localmodel.PickModel(hw)
		if !ok {
			return fmt.Errorf("no local model in the curated list fits this hardware; pass --model to force one")
		}
		spec = picked
	}

	alreadyInstalled, err := mgr.IsInstalled(ctx, spec.Name)
	if err != nil {
		return fmt.Errorf("check installed models: %w", err)
	}

	numCtx := localmodel.MinRecommendedNumCtx
	if spec.KVCachePerKTokenMB > 0 {
		// Only compute a hardware-based context window when we have a real
		// sizing profile for this model (catalog entry, whether auto-picked
		// or forced via --model). A truly custom model name with no profile
		// falls back to the safe floor rather than guessing at its VRAM cost.
		numCtx = localmodel.RecommendNumCtx(spec, hw.VRAMTotalMB)
	}

	fmt.Printf("Model:  %s\n", spec.Name)
	if spec.Description != "" {
		fmt.Printf("        %s\n", spec.Description)
	}
	if spec.DiskGB > 0 {
		fmt.Printf("Size:   ~%.1f GB download\n", spec.DiskGB)
	}
	fmt.Printf("Context: %d tokens (num_ctx, sized to fit detected VRAM)\n", numCtx)
	if alreadyInstalled {
		fmt.Println("Status: already downloaded")
	} else {
		fmt.Println("Status: not yet downloaded")
	}
	fmt.Println()
	fmt.Println("This will:")
	if !alreadyInstalled {
		fmt.Printf("  1. Download %s via the local Ollama daemon (~%.1f GB)\n", spec.Name, spec.DiskGB)
	}
	fmt.Println("  2. Set local_model_enabled=true and local_model_name=" + spec.Name + " in config")
	fmt.Println("  3. Every goal will try this model first, falling back to your remote")
	fmt.Println("     provider (" + providerOrDefault(cfg.LLMProvider) + ") only if the local model fails")
	fmt.Println()
	fmt.Println("Revert anytime with: hyperi models disable")

	if !skipConfirm {
		if !confirm("Proceed?") {
			fmt.Println("Aborted — no changes made.")
			return nil
		}
	}

	if !alreadyInstalled {
		fmt.Printf("\nPulling %s...\n", spec.Name)
		lastPct := -1
		err := mgr.PullModel(ctx, spec.Name, func(p localmodel.PullProgress) {
			if p.Total > 0 {
				pct := int(float64(p.Completed) / float64(p.Total) * 100)
				if pct != lastPct {
					fmt.Printf("\r  %s: %d%%", p.Status, pct)
					lastPct = pct
				}
			} else if p.Status != "" {
				fmt.Printf("\r  %s", p.Status)
			}
		})
		fmt.Println()
		if err != nil {
			return fmt.Errorf("pull model: %w", err)
		}
		fmt.Println("Download complete.")
	}

	cfgPath := defaultConfigPath()
	cfg.LocalModelEnabled = true
	cfg.LocalModelName = spec.Name
	cfg.OllamaBaseURL = baseURL
	cfg.LocalModelNumCtx = numCtx
	cfg.LocalModelConfirmedAt = time.Now()
	if err := config.Save(cfgPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("\nLocal model enabled: %s\n", spec.Name)
	fmt.Println("Restart 'hyperi serve' for this to take effect (if already running).")
	return nil
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func buildModelsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current local-model configuration and daemon reachability",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}

			fmt.Printf("local_model_enabled: %v\n", cfg.LocalModelEnabled)
			fmt.Printf("local_model_name:    %s\n", orNone(cfg.LocalModelName))
			fmt.Printf("ollama_base_url:     %s\n", orDefault(cfg.OllamaBaseURL, localmodel.DefaultBaseURL))
			if cfg.LocalModelNumCtx > 0 {
				fmt.Printf("num_ctx:             %d tokens\n", cfg.LocalModelNumCtx)
			} else {
				fmt.Println("num_ctx:             auto (recomputed from hardware at server startup)")
			}
			fmt.Printf("keep_alive:          %s\n", orDefault(cfg.LocalModelKeepAlive, "30m"))
			fmt.Printf("goal_timeout:        %d minutes\n", cfg.GoalTimeoutMinutes)
			fmt.Printf("max_tool_iterations: %d\n", cfg.MaxToolIterations)
			if !cfg.LocalModelConfirmedAt.IsZero() {
				fmt.Printf("confirmed_at:        %s\n", cfg.LocalModelConfirmedAt.Format(time.RFC3339))
			}
			fmt.Printf("remote_fallback:     %s\n", providerOrDefault(cfg.LLMProvider))

			fmt.Println()
			baseURL := orDefault(cfg.OllamaBaseURL, localmodel.DefaultBaseURL)
			mgr := localmodel.NewManager(baseURL)
			ctx := context.Background()
			if !mgr.Available(ctx) {
				fmt.Printf("ollama daemon:       not reachable at %s\n", baseURL)
				return nil
			}
			fmt.Println("ollama daemon:       reachable")

			installed, err := mgr.InstalledModels(ctx)
			if err != nil {
				fmt.Printf("installed models:    error: %v\n", err)
				return nil
			}
			if len(installed) == 0 {
				fmt.Println("installed models:    (none)")
			} else {
				fmt.Printf("installed models:    %s\n", strings.Join(installed, ", "))
			}
			return nil
		},
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func buildModelsEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Re-enable local model use after a previous 'disable' (no re-download)",
		Long: `Sets local_model_enabled=true using the previously-configured
local_model_name, without re-pulling or re-confirming. Use 'hyperi models
setup' instead if you haven't set up a local model before, or want to change
which model is used.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}
			if cfg.LocalModelName == "" {
				return fmt.Errorf("no local model configured yet — run 'hyperi models setup' first")
			}
			cfg.LocalModelEnabled = true
			if err := config.Save(defaultConfigPath(), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Printf("Local model re-enabled: %s\n", cfg.LocalModelName)
			return nil
		},
	}
}

func buildModelsDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Disable local model use immediately; revert to remote-only",
		Long: `Sets local_model_enabled=false. This is the "easy revert" — it takes
effect immediately (after restarting 'hyperi serve') and does not delete the
downloaded model or any other config, so 'hyperi models enable' can turn it
back on instantly with no re-download.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}
			cfg.LocalModelEnabled = false
			if err := config.Save(defaultConfigPath(), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Println("Local model disabled. All goals will now use the remote provider only.")
			fmt.Println("Restart 'hyperi serve' for this to take effect (if already running).")
			return nil
		},
	}
}

func buildModelsRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Disable local mode AND delete the downloaded model from Ollama",
		Long: `Like 'disable', but also deletes the model from the Ollama daemon via
DELETE /api/delete, freeing its disk space. Use this if you want to fully
undo 'hyperi models setup', not just pause it.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig("")
			if err != nil {
				return err
			}
			if cfg.LocalModelName == "" {
				fmt.Println("No local model configured — nothing to remove.")
				return nil
			}
			if !yes && !confirm(fmt.Sprintf("Delete model %q from Ollama and disable local mode?", cfg.LocalModelName)) {
				fmt.Println("Aborted — no changes made.")
				return nil
			}

			baseURL := orDefault(cfg.OllamaBaseURL, localmodel.DefaultBaseURL)
			mgr := localmodel.NewManager(baseURL)
			if err := mgr.DeleteModel(context.Background(), cfg.LocalModelName); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to delete model from Ollama: %v\n", err)
			}

			cfg.LocalModelEnabled = false
			cfg.LocalModelName = ""
			if err := config.Save(defaultConfigPath(), cfg); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			fmt.Println("Local model removed and disabled.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}
