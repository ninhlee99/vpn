package diagnostics

import "testing"

func TestMTUFailure(t *testing.T) {
	tests := []struct {
		name   string
		probes []MTUProbe
		want   bool
	}{
		{
			name:   "1500 fails but tunnel default succeeds",
			probes: []MTUProbe{{Size: 1500, OK: false}, {Size: 1492, OK: true}, {Size: 1400, OK: true}},
			want:   false,
		},
		{
			name:   "default tunnel MTU fails",
			probes: []MTUProbe{{Size: 1500, OK: false}, {Size: 1400, OK: false}, {Size: 1380, OK: true}},
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mtuFailure(tt.probes); got != tt.want {
				t.Fatalf("mtuFailure() = %v, want %v", got, tt.want)
			}
		})
	}
}
