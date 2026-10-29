//go:build linux

package pve

import (
	"maps"
	"testing"
	"time"
)

func TestPVEConfigAndCounterSemantics(t *testing.T) {
	hostCPUs, err := hostCPUCount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	config, err := parseConfig(101, "name: guest\ncores: 2\nmemory: current=2048\nagent: enabled=1,type=virtio\nnet0: virtio=AA:BB:CC:DD:EE:01,rate=12.5\nnet1: virtio=AA:BB:CC:DD:EE:02\nnet2: virtio=AA:BB:CC:DD:EE:03,rate=25\n[PENDING]\ncores: 8\nnet0: virtio=AA:BB:CC:DD:EE:01,rate=100\n[snapshot]\nnet3: virtio=AA:BB:CC:DD:EE:04,rate=50\n", hostCPUs)
	if err != nil {
		t.Fatal(err)
	}
	vm := config.vm
	if vm.ID != 101 || vm.Name != "guest" || *vm.CPUs != min(int64(2), hostCPUs) || *vm.MemoryLimit != 2<<30 || config.agent == "" || vm.CPU != nil || vm.Memory != nil {
		t.Fatalf("config invented live observations or applied pending changes: %+v", config)
	}
	if !maps.Equal(vm.NetRateLimits, map[string]float64{"net0": 104.86, "net1": 0, "net2": 209.72}) {
		t.Fatalf("current NIC rate limits: %v", vm.NetRateLimits)
	}
	template, err := parseConfig(102, "template: 1\nagent: enabled=0,type=virtio\n", hostCPUs)
	if err != nil || !template.vm.Template || template.agent != "" {
		t.Fatalf("template/disabled agent: %+v, %v", template, err)
	}
	for _, input := range []struct {
		raw  string
		cpus int64
	}{
		{"cores: 16\n", 8},
		{"cores: 16\nvcpus: 4\n", 4},
	} {
		config, err := parseConfig(103, input.raw, 8)
		if err != nil || *config.vm.CPUs != input.cpus {
			t.Fatalf("VM CPU allocation: %+v, %v", config, err)
		}
	}
	if _, err := networkRateLimits("net0: virtio=AA:BB:CC:DD:EE:01,rate=-1\n"); err == nil {
		t.Fatal("accepted negative NIC rate")
	}

	sample, err := parseProcess([]byte("123 (kvm (guest)) S 1 2 3 4 5 6 7 8 9 10 100 50 0 0 0 0 0 0 1000"))
	if err != nil || sample.ticks != 150 || sample.start != 1000 {
		t.Fatalf("process counters: %+v, %v", sample, err)
	}
	sample.pid, sample.at = 123, time.Now()
	if cpuRatio(processSample{}, sample, 2) != nil {
		t.Fatal("first CPU sample became zero")
	}
	next := sample
	next.at, next.ticks = sample.at.Add(3*time.Second), sample.ticks+300
	if ratio := cpuRatio(sample, next, 2); ratio == nil || *ratio != 0.5 {
		t.Fatalf("CPU normalization: %v", ratio)
	}
	next.start++
	if cpuRatio(sample, next, 2) != nil {
		t.Fatal("CPU delta crossed a VM restart")
	}
}
