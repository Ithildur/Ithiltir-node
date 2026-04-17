//go:build linux

package collect

import (
	"encoding/json"
	"fmt"
	"time"
)

type lsblkOutput struct {
	Blockdevices []lsblkDev `json:"blockdevices"`
}

type lsblkDev struct {
	Name       string     `json:"name"`
	KName      string     `json:"kname"`
	Type       string     `json:"type"`
	Size       string     `json:"size"`
	Model      string     `json:"model"`
	Serial     string     `json:"serial"`
	Rota       string     `json:"rota"`
	Mountpoint string     `json:"mountpoint"`
	Fstype     string     `json:"fstype"`
	Children   []lsblkDev `json:"children"`
}

func readLsblkTree() (*lsblkOutput, error) {
	if !commandExists("lsblk") {
		return nil, fmt.Errorf("lsblk not found")
	}
	b, err := runCmd(2*time.Second, "lsblk",
		"-b", "--nosuffix",
		"-J",
		"-o", "NAME,KNAME,TYPE,SIZE,MODEL,SERIAL,ROTA,MOUNTPOINT,FSTYPE",
	)
	if err != nil {
		return nil, err
	}
	var j lsblkOutput
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	return &j, nil
}
