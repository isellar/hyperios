package localmodel

import "testing"

func TestPickModel_GPUFit(t *testing.T) {
	cases := []struct {
		name      string
		hw        Hardware
		wantModel string
		wantOK    bool
	}{
		{
			name:      "16GB VRAM picks 7b (14b needs 11000, 90% of 16303 leaves ~14672 which fits 14b too)",
			hw:        Hardware{VRAMTotalMB: 16303, SystemRAMTotalMB: 131739},
			wantModel: "qwen2.5:14b",
			wantOK:    true,
		},
		{
			name:      "8GB VRAM picks 7b",
			hw:        Hardware{VRAMTotalMB: 8192, SystemRAMTotalMB: 32000},
			wantModel: "qwen2.5:7b",
			wantOK:    true,
		},
		{
			name:      "24GB+ VRAM picks 32b",
			hw:        Hardware{VRAMTotalMB: 24576, SystemRAMTotalMB: 64000},
			wantModel: "qwen2.5:32b",
			wantOK:    true,
		},
		{
			name:      "no GPU, 16GB RAM picks by RAM (3b or 7b)",
			hw:        Hardware{SystemRAMTotalMB: 16000},
			wantModel: "qwen2.5:3b",
			wantOK:    true,
		},
		{
			name:      "no GPU, tiny RAM fits nothing",
			hw:        Hardware{SystemRAMTotalMB: 2000},
			wantModel: "",
			wantOK:    false,
		},
		{
			name:      "small VRAM GPU falls through to RAM-based pick",
			hw:        Hardware{VRAMTotalMB: 2000, SystemRAMTotalMB: 16000},
			wantModel: "qwen2.5:3b",
			wantOK:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, ok := PickModel(tc.hw)
			if ok != tc.wantOK {
				t.Fatalf("PickModel() ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && spec.Name != tc.wantModel {
				t.Errorf("PickModel() = %q, want %q", spec.Name, tc.wantModel)
			}
		})
	}
}

func TestCatalog_AscendingBySize(t *testing.T) {
	for i := 1; i < len(Catalog); i++ {
		if Catalog[i].DiskGB <= Catalog[i-1].DiskGB {
			t.Errorf("Catalog not ascending by DiskGB at index %d: %v <= %v",
				i, Catalog[i].DiskGB, Catalog[i-1].DiskGB)
		}
		if Catalog[i].MinRAMMB <= Catalog[i-1].MinRAMMB {
			t.Errorf("Catalog not ascending by MinRAMMB at index %d", i)
		}
	}
}

func TestHardware_HasGPU(t *testing.T) {
	if (Hardware{}).HasGPU() {
		t.Error("zero-value Hardware should report HasGPU() == false")
	}
	if !(Hardware{VRAMTotalMB: 8192}).HasGPU() {
		t.Error("Hardware with VRAMTotalMB > 0 should report HasGPU() == true")
	}
}

func TestRecommendNumCtx(t *testing.T) {
	specByName := func(name string) ModelSpec {
		for _, s := range Catalog {
			if s.Name == name {
				return s
			}
		}
		t.Fatalf("no catalog entry named %q", name)
		return ModelSpec{}
	}

	cases := []struct {
		name        string
		model       string
		vramTotalMB int
		wantAtLeast int
		wantAtMost  int
	}{
		{
			name:        "14b on 16GB VRAM gets a real multi-thousand-token window",
			model:       "qwen2.5:14b",
			vramTotalMB: 16303,
			wantAtLeast: 8192,
			wantAtMost:  32768,
		},
		{
			name:        "7b on 16GB VRAM gets a large window (small model, lots of headroom)",
			model:       "qwen2.5:7b",
			vramTotalMB: 16303,
			wantAtLeast: 32768,
			wantAtMost:  131072,
		},
		{
			name:        "32b on tight 22GB VRAM falls back to the safe floor",
			model:       "qwen2.5:32b",
			vramTotalMB: 22000,
			wantAtLeast: MinRecommendedNumCtx,
			wantAtMost:  MinRecommendedNumCtx,
		},
		{
			name:        "zero VRAM never returns less than the safe floor",
			model:       "qwen2.5:3b",
			vramTotalMB: 0,
			wantAtLeast: MinRecommendedNumCtx,
			wantAtMost:  MinRecommendedNumCtx,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RecommendNumCtx(specByName(tc.model), tc.vramTotalMB)
			if got < tc.wantAtLeast || got > tc.wantAtMost {
				t.Errorf("RecommendNumCtx(%s, %d) = %d, want in [%d, %d]",
					tc.model, tc.vramTotalMB, got, tc.wantAtLeast, tc.wantAtMost)
			}
			if got < MinRecommendedNumCtx {
				t.Errorf("RecommendNumCtx() = %d, should never be below MinRecommendedNumCtx (%d)",
					got, MinRecommendedNumCtx)
			}
		})
	}
}

func TestRecommendNumCtx_NeverBelowFloor(t *testing.T) {
	// Fuzz a range of VRAM values across all catalog entries and assert the
	// floor invariant always holds, since a silent-truncation regression
	// here would be a correctness bug that's easy to miss visually.
	for _, spec := range Catalog {
		for vram := 0; vram <= 65536; vram += 1024 {
			got := RecommendNumCtx(spec, vram)
			if got < MinRecommendedNumCtx {
				t.Fatalf("RecommendNumCtx(%s, %d) = %d, below floor %d",
					spec.Name, vram, got, MinRecommendedNumCtx)
			}
		}
	}
}
