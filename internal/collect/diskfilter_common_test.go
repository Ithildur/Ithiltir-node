package collect

import "testing"

func TestParseZFSIORatesSkipsFirstSamplePerPool(t *testing.T) {
	got := parseZFSIORates(`
tank 0 0 100 200 1000 2000
data 0 0 50 60 500 600
tank 0 0 1 4 10 40
data 0 0 7 9 70 90
tank 0 0 2 5 20 50
data 0 0 8 10 80 100
tank 0 0 3 6 30 60
data 0 0 9 11 90 110
`)

	tank := got["tank"]
	if tank.readIOPS != 2 || tank.writeIOPS != 5 || tank.readBps != 20 || tank.writeBps != 50 {
		t.Fatalf("tank rates = %+v, want averages after first sample", tank)
	}

	dataPool := got["data"]
	if dataPool.readIOPS != 8 || dataPool.writeIOPS != 10 || dataPool.readBps != 80 || dataPool.writeBps != 100 {
		t.Fatalf("data rates = %+v, want averages after first sample", dataPool)
	}
}
