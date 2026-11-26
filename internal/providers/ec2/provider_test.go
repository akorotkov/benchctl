package ec2

import (
	"context"
	"testing"

	"github.com/dbarena/benchctl/internal/config"
)

func TestArchFromInstanceType(t *testing.T) {
	tests := []struct {
		instanceType string
		want         string
	}{
		{"c8gd.xlarge", "arm64"},
		{"t4g.small", "arm64"},
		{"m7g.large", "arm64"},
		{"c5.xlarge", "amd64"},
		{"m6i.large", "amd64"},
		{"t3.micro", "amd64"},
	}

	for _, tt := range tests {
		t.Run(tt.instanceType, func(t *testing.T) {
			if got := archFromInstanceType(tt.instanceType); got != tt.want {
				t.Errorf("archFromInstanceType(%q) = %q, want %q", tt.instanceType, got, tt.want)
			}
		})
	}
}

func TestNewOnTarget_IsAnExistingHostDriver(t *testing.T) {
	p := NewOnTarget(&config.Config{})
	// Teardown must not run tofu (there is no work dir) and must not fail.
	if err := p.Teardown(context.Background(), nil); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
}
