// Package distribution stores the deployment marker appended to a downloadable agent.
package distribution

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
)

var magic = []byte("\x00MEOWPROBE-DISTRIBUTION-V1\x00")

type Config struct {
	Server          string `json:"server"`
	RegistrationKey string `json:"registration_key"`
}

func Encode(base []byte, c Config) ([]byte, error) {
	if c.Server == "" || c.RegistrationKey == "" {
		return nil, fmt.Errorf("distribution server and registration key are required")
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if len(payload) > 8192 {
		return nil, fmt.Errorf("distribution config is too large")
	}
	out := make([]byte, 0, len(base)+len(magic)+4+len(payload))
	out = append(out, base...)
	out = append(out, magic...)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	out = append(out, size[:]...)
	out = append(out, payload...)
	return out, nil
}

func Decode(data []byte) (Config, bool) {
	var c Config
	index := bytes.LastIndex(data, magic)
	if index < 0 {
		return c, false
	}
	start := index + len(magic)
	if start+4 > len(data) {
		return c, false
	}
	size := int(binary.BigEndian.Uint32(data[start : start+4]))
	payload := start + 4
	if size <= 0 || size > 8192 || payload+size != len(data) {
		return c, false
	}
	if json.Unmarshal(data[payload:payload+size], &c) != nil || c.Server == "" || c.RegistrationKey == "" {
		return Config{}, false
	}
	return c, true
}

func ReadExecutable() (Config, bool) {
	exe, err := os.Executable()
	if err != nil {
		return Config{}, false
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return Config{}, false
	}
	return Decode(data)
}
