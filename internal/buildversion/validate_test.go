package buildversion

import "testing"

func TestValid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"plain release", "1.2.3", true},
		{"release with build", "1.2.3+build.7", true},
		{"zero release", "0.0.0", true},
		{"pre release", "1.2.3-rc.1", true},
		{"pre release with build", "1.2.3-alpha.1+build.7", true},
		{"missing patch", "1.2", false},
		{"v prefix", "v1.2.3", false},
		{"leading major zero", "01.2.3", false},
		{"leading minor zero", "1.02.3", false},
		{"leading patch zero", "1.2.03", false},
		{"empty prerelease", "1.2.3-", false},
		{"empty prerelease identifier", "1.2.3-alpha..1", false},
		{"numeric prerelease leading zero", "1.2.3-01", false},
		{"empty build", "1.2.3+", false},
		{"empty build identifier", "1.2.3+build..7", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Valid(tt.in); got != tt.want {
				t.Fatalf("Valid(%q) = %t, want %t", tt.in, got, tt.want)
			}
		})
	}
}

func TestPrerelease(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"plain release", "1.2.3", false},
		{"release with build", "1.2.3+build.7", false},
		{"release with hyphenated build", "1.2.3+build-meta.7", false},
		{"pre release", "1.2.3-rc.1", true},
		{"pre release with build", "1.2.3-rc.1+build.7", true},
		{"invalid", "v1.2.3-rc.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Prerelease(tt.in); got != tt.want {
				t.Fatalf("Prerelease(%q) = %t, want %t", tt.in, got, tt.want)
			}
		})
	}
}
