package localmodel

// ModelSpec describes one curated local model option.
type ModelSpec struct {
	// Name is the Ollama model tag to pull/run, e.g. "qwen2.5:7b".
	Name string
	// DiskGB is the approximate download/disk size in GB.
	DiskGB float64
	// MinVRAMMB is the minimum GPU VRAM recommended to run this model with
	// reasonable speed at a modest (~8k token) context. 0 means it's viable
	// CPU-only (small enough).
	MinVRAMMB int
	// MinRAMMB is the minimum system RAM recommended (covers CPU-only
	// inference and the VRAM-insufficient-fallback-to-CPU case).
	MinRAMMB int
	// BaseVRAMMB is the approximate VRAM used by the model weights
	// themselves (Q4_K_M quantization) plus a negligible/near-zero context,
	// i.e. the fixed cost before any KV cache. Used by RecommendNumCtx to
	// figure out how much VRAM is left over for context.
	BaseVRAMMB int
	// KVCachePerKTokenMB is the approximate additional VRAM (in MB) consumed
	// by the KV cache per 1024 tokens of context window, at this model's
	// size. These are rough, size-scaled approximations (real cost also
	// depends on GQA head config and KV cache quantization), intended to
	// keep num_ctx selection conservative rather than exact.
	KVCachePerKTokenMB int
	// Description is a short human-readable summary for CLI output.
	Description string
}

// Catalog is the curated, ascending-by-size list of local models HyperiOS
// will consider. All of these are Qwen2.5-Instruct models, which have
// well-tested native tool-calling support in Ollama (verified across the
// whole size range, unlike many other model families where tool-calling
// reliability varies a lot). Keeping this list small and curated (rather
// than exposing the entire Ollama library) means we can make reasonably
// confident claims about how well each option will actually perform in the
// agent's tool-use loop.
//
// Sizes chosen to span "modest laptop CPU-only" through "single high-end
// consumer GPU":
//   - qwen2.5:3b  — runs acceptably on CPU alone; lowest quality tool-use
//   - qwen2.5:7b  — good default for 8GB+ VRAM GPUs or 16GB+ RAM CPU-only
//   - qwen2.5:14b — needs a decent GPU (12-16GB VRAM) or a lot of RAM
//   - qwen2.5:32b — needs a high-end consumer GPU (24GB+ VRAM)
var Catalog = []ModelSpec{
	{
		Name:               "qwen2.5:3b",
		DiskGB:             2.0,
		MinVRAMMB:          0,
		MinRAMMB:           6000,
		BaseVRAMMB:         2200,
		KVCachePerKTokenMB: 60,
		Description:        "Smallest option; runs on CPU-only machines with 8GB+ RAM. Weakest tool-use reliability.",
	},
	{
		Name:               "qwen2.5:7b",
		DiskGB:             4.7,
		MinVRAMMB:          6000,
		MinRAMMB:           12000,
		BaseVRAMMB:         5100,
		KVCachePerKTokenMB: 140,
		Description:        "Good default: solid tool-use reliability, runs on most 8GB+ GPUs or 16GB+ RAM CPU-only.",
	},
	{
		Name:               "qwen2.5:14b",
		DiskGB:             9.0,
		MinVRAMMB:          11000,
		MinRAMMB:           20000,
		BaseVRAMMB:         9500,
		KVCachePerKTokenMB: 280,
		Description:        "Stronger reasoning; needs a 12GB+ VRAM GPU or a lot of system RAM if CPU-only.",
	},
	{
		Name:               "qwen2.5:32b",
		DiskGB:             19.0,
		MinVRAMMB:          22000,
		MinRAMMB:           40000,
		BaseVRAMMB:         20000,
		KVCachePerKTokenMB: 640,
		Description:        "Best local quality in this list; needs a high-end consumer GPU (24GB+ VRAM).",
	},
}

// contextSizeSteps are the num_ctx values RecommendNumCtx will choose between
// (common power-of-two-ish steps used across the Ollama ecosystem).
var contextSizeSteps = []int{4096, 8192, 16384, 32768, 65536, 131072}

// MinRecommendedNumCtx is the floor RecommendNumCtx will ever return. Below
// this, multi-step tool-use loops reliably run out of room (system prompt +
// tool schemas + a few rounds of command output routinely exceeds 4k tokens)
// and Ollama's silent head-truncation behavior (see package docs) starts
// dropping the system prompt / early tool results without any error.
const MinRecommendedNumCtx = 4096

// RecommendNumCtx picks a context-window size (Ollama's num_ctx) for spec
// given availableVRAMMB of headroom beyond the model's base weight VRAM
// (BaseVRAMMB) — i.e. pass hw.VRAMTotalMB, not a pre-subtracted value.
//
// This exists because Ollama does NOT set a safe default: depending on
// version/platform it can default as low as 2048-4096 tokens, and when the
// context fills up it silently drops the OLDEST messages (including the
// system prompt and early tool call results) with no error surfaced to the
// caller. For a multi-step tool-use agent loop — system prompt + tool
// schemas + N rounds of shell output — this is exactly the failure mode
// that would quietly corrupt longer-running goals. We pick a generous,
// explicit num_ctx up front instead of trusting the daemon default.
//
// Returns MinRecommendedNumCtx if there isn't even enough headroom for that
// (the caller should still proceed — Ollama will just run slower / rely more
// on CPU offload rather than fail outright — but a warning is warranted).
func RecommendNumCtx(spec ModelSpec, availableVRAMMB int) int {
	// Same 10% safety margin as PickModel, leaving room for the OS/desktop
	// environment/other GPU consumers rather than assuming every last MB is
	// available to the model.
	usable := int(float64(availableVRAMMB) * 0.9)
	headroom := usable - spec.BaseVRAMMB
	if headroom <= 0 || spec.KVCachePerKTokenMB <= 0 {
		return MinRecommendedNumCtx
	}

	maxKTokens := headroom / spec.KVCachePerKTokenMB
	maxTokens := maxKTokens * 1024

	best := MinRecommendedNumCtx
	for _, step := range contextSizeSteps {
		if step <= maxTokens {
			best = step
		}
	}
	return best
}

// PickModel selects the largest (highest-quality) model in Catalog that
// comfortably fits the detected hardware, preferring GPU-fit over CPU-fit.
// Returns (spec, true) on success, or (zero, false) if even the smallest
// model doesn't fit the detected RAM.
//
// Selection logic:
//  1. If a GPU is present, pick the largest model whose MinVRAMMB fits within
//     hw.VRAMTotalMB (leaving ~10% headroom for the OS/other processes).
//  2. Otherwise (or if no GPU model fits), fall back to picking by system RAM
//     using MinRAMMB, still preferring the largest model that fits.
func PickModel(hw Hardware) (ModelSpec, bool) {
	if hw.HasGPU() {
		usableVRAM := int(float64(hw.VRAMTotalMB) * 0.9)
		if spec, ok := pickBestFit(usableVRAM, func(m ModelSpec) int { return m.MinVRAMMB }); ok {
			return spec, true
		}
	}

	usableRAM := int(float64(hw.SystemRAMTotalMB) * 0.7)
	return pickBestFit(usableRAM, func(m ModelSpec) int { return m.MinRAMMB })
}

// pickBestFit returns the largest-indexed (by Catalog's ascending-size
// ordering) entry whose requirement(m) <= budget.
func pickBestFit(budget int, requirement func(ModelSpec) int) (ModelSpec, bool) {
	best := -1
	for i, m := range Catalog {
		if requirement(m) <= budget {
			best = i
		}
	}
	if best < 0 {
		return ModelSpec{}, false
	}
	return Catalog[best], true
}
