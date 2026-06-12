//go:build linux

package collect

import (
	"testing"

	"Ithiltir-node/internal/metrics"
)

func TestParsePressureResource(t *testing.T) {
	got, err := parsePressureResource("some avg10=1.25 avg60=0.50 avg300=0.10 total=123\nfull avg10=0.25 avg60=0.10 avg300=0.01 total=7\n")
	if err != nil {
		t.Fatalf("parsePressureResource() error = %v", err)
	}
	if got.Status != metrics.StatusOK {
		t.Fatalf("status = %q, want %q", got.Status, metrics.StatusOK)
	}
	if got.Some == nil {
		t.Fatal("some = nil")
	}
	if got.Some.Avg10 != 1.25 || got.Some.Avg60 != 0.50 || got.Some.Avg300 != 0.10 || got.Some.Total != 123 {
		t.Fatalf("some = %+v", got.Some)
	}
	if got.Full == nil {
		t.Fatal("full = nil")
	}
	if got.Full.Avg10 != 0.25 || got.Full.Avg60 != 0.10 || got.Full.Avg300 != 0.01 || got.Full.Total != 7 {
		t.Fatalf("full = %+v", got.Full)
	}
}

func TestParsePressureResourceAllowsMissingFull(t *testing.T) {
	got, err := parsePressureResource("some avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	if err != nil {
		t.Fatalf("parsePressureResource() error = %v", err)
	}
	if got.Status != metrics.StatusOK {
		t.Fatalf("status = %q, want %q", got.Status, metrics.StatusOK)
	}
	if got.Some == nil {
		t.Fatal("some = nil")
	}
	if got.Full != nil {
		t.Fatalf("full = %+v, want nil", got.Full)
	}
}

func TestParsePressureResourceRejectsMissingField(t *testing.T) {
	if _, err := parsePressureResource("some avg10=1.00 avg60=0.50 total=123\n"); err == nil {
		t.Fatal("parsePressureResource() error = nil, want error")
	}
}
