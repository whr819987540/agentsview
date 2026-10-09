package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsNewerRollingBuild(t *testing.T) {
	tests := []struct {
		name             string
		current, running string
		want             bool
	}{
		{"larger N same base", "v0.44.0-40-g1a2b3c4d", "v0.44.0-39-g0f0f0f0f", true},
		{"smaller N same base", "v0.44.0-39-g0f0f0f0f", "v0.44.0-40-g1a2b3c4d", false},
		{"N 10 beats 9", "v0.44.0-10-gabc1234", "v0.44.0-9-gdef5678", true},
		{"N 9 loses to 10", "v0.44.0-9-gdef5678", "v0.44.0-10-gabc1234", false},
		{"N 100 beats 99", "v0.44.0-100-gabc1234", "v0.44.0-99-gdef5678", true},
		{"N 99 loses to 100", "v0.44.0-99-gdef5678", "v0.44.0-100-gabc1234", false},
		{"newer patch with smaller N", "v0.44.1-1-gabc1234", "v0.44.0-120-gdef5678", true},
		{"newer minor with smaller N", "v0.45.0-2-gabc1234", "v0.44.0-120-gdef5678", true},
		{"older minor with larger N", "v0.44.0-120-gdef5678", "v0.45.0-2-gabc1234", false},
		{"newer major", "v1.0.0", "v0.99.0-500-gabc1234", true},
		{"minor 10 beats 9", "v0.10.0", "v0.9.0-200-gabc1234", true},
		{"rolling current past stable running", "v0.44.0-40-g1a2b3c4d", "v0.44.0", true},
		{"stable current at running's base", "v0.44.0", "v0.44.0-40-g1a2b3c4d", false},
		{"stable current past older rolling", "v0.45.0", "v0.44.0-40-g1a2b3c4d", true},
		{"stable both newer", "v0.45.0", "v0.44.0", true},
		{"equal rolling", "v0.44.0-40-g1a2b3c4d", "v0.44.0-40-g1a2b3c4d", false},
		{"equal position different hash", "v0.44.0-40-gaaaaaaaa", "v0.44.0-40-gbbbbbbbb", false},
		{"equal stable", "v0.44.0", "v0.44.0", false},
		{"zero N equals tag", "v0.44.0-0-gabc1234", "v0.44.0", false},
		{"without v prefix", "0.44.0-40-g1a2b3c4d", "v0.44.0-39-g0f0f0f0f", true},
		{"malformed current dev", "dev", "v0.44.0", false},
		{"malformed running dev", "v0.44.0-40-g1a2b3c4d", "dev", false},
		{"empty current", "", "v0.44.0", false},
		{"empty running", "v0.44.0-40-g1a2b3c4d", "", false},
		{"bare commit hash", "1a2b3c4d", "v0.44.0", false},
		{"prerelease tag", "v0.45.0-rc1", "v0.44.0", false},
		{"prerelease tag with commits", "v0.45.0-rc1-3-gabc1234", "v0.44.0", false},
		{"missing hash", "v0.44.0-40", "v0.44.0", false},
		{"non-hex hash", "v0.44.0-40-gxyz", "v0.44.0", false},
		{"two-part version", "v0.45", "v0.44.0", false},
		{"surrounding space", " v0.45.0", "v0.44.0", false},
		{"overflowing N", "v0.44.0-99999999999999999999-gabc1234", "v0.44.0", false},
		{"dirty current", "v0.44.0-41-gabc1234-dirty", "v0.44.0-40-gdef5678", false},
		{"dirty running", "v0.44.0-41-gabc1234", "v0.44.0-40-gdef5678-dirty", false},
		{"dirty stable current", "v0.45.0-dirty", "v0.44.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsNewerRollingBuild(tt.current, tt.running))
		})
	}
}

func TestIsRollingBuildVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"v0.44.0", true},
		{"v0.44.0-40-g1a2b3c4d", true},
		{"0.44.0-40-g1a2b3c4d", true},
		{"v0.44.0-40-g1a2b3c4d-dirty", false},
		{"v0.44.0-dirty", false},
		{"v0.45.0-rc1", false},
		{"1a2b3c4d", false},
		{"dev", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			assert.Equal(t, tt.want, IsRollingBuildVersion(tt.version))
		})
	}
}
