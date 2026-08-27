package units

import "testing"

func TestBytesRendersManifestReadyQuantities(t *testing.T) {
	cases := map[int64]string{
		0:         "0",
		512:       "512",
		10 * Mi:   "10Mi",
		256 * Mi:  "256Mi",
		1536 * Mi: "1.5Gi",
		16 * Gi:   "16Gi",
		2 * Ti:    "2Ti",
	}
	for in, want := range cases {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestCPUMatchesKubectlNotation(t *testing.T) {
	cases := map[int64]string{0: "0", 250: "250m", 1000: "1", 2500: "2500m", 4000: "4"}
	for in, want := range cases {
		if got := CPU(in); got != want {
			t.Errorf("CPU(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRoundUpNeverRoundsDown(t *testing.T) {
	if got := RoundUpMi(1); got != Mi {
		t.Errorf("RoundUpMi(1) = %d, want %d", got, Mi)
	}
	if got := RoundUpMi(Mi); got != Mi {
		t.Errorf("RoundUpMi(1Mi) = %d, want %d", got, Mi)
	}
	if got := RoundUpMi(Mi + 1); got != 2*Mi {
		t.Errorf("RoundUpMi(1Mi+1) = %d, want %d", got, 2*Mi)
	}
	if got := RoundUpMilli(1); got != 10 {
		t.Errorf("RoundUpMilli(1) = %d, want 10", got)
	}
}
