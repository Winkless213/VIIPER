package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSyntheticCaptureIsExplicitlyLabeledAndPreservesRear(t *testing.T) {
	var output bytes.Buffer
	if err := syntheticCapture(&output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var block audioMessage
	if err := decoder.Decode(&block); err != nil {
		t.Fatal(err)
	}
	if block.Source != "synthetic" || block.SampleRate != 48000 || block.Frames != 48 || len(block.PCM) != 192 {
		t.Fatalf("invalid capture: %+v", block)
	}
	if !bytes.Equal(block.PCM[:8], []byte{0xe8, 3, 0x18, 0xfc, 0xe9, 3, 0x17, 0xfc}) {
		t.Fatalf("wrong channels: %v", block.PCM[:8])
	}
}
