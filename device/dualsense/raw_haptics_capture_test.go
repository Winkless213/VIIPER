package dualsense

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestRawHapticsCaptureKeepsRearChannelsBeforeBluetoothAssembly(t *testing.T) {
	d, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	capture := d.StartRawHapticsCapture()
	d.SetInterfaceAltSetting(InterfaceHapticsAudio, 1)
	generation := d.IsoOutGeneration(EndpointHapticsAudioOut)
	source := make([]byte, 48*8)
	want := make([]byte, 48*4)
	for frame := 0; frame < 48; frame++ {
		binary.LittleEndian.PutUint16(source[frame*8:], 30000)
		binary.LittleEndian.PutUint16(source[frame*8+2:], 20000)
		binary.LittleEndian.PutUint16(source[frame*8+4:], uint16(1000+frame))
		binary.LittleEndian.PutUint16(source[frame*8+6:], uint16(4000-frame))
		copy(want[frame*4:], source[frame*8+4:frame*8+8])
	}
	if !d.handleHapticsAudioOutGeneration(source, generation) {
		t.Fatal("input rejected")
	}
	clear(source)
	block, ok := capture.Next()
	if !ok || block.Frames != 48 || block.Generation != generation || block.FirstFrame != 0 || block.ReceivedAt.IsZero() {
		t.Fatalf("invalid first 1ms block: %+v, ok=%v", block, ok)
	}
	if !bytes.Equal(block.PCM[:block.Frames*4], want) {
		t.Fatal("rear PCM changed or aliased input")
	}
}

func TestRawHapticsCaptureStopClearsQueueAndDisablesFurtherCapture(t *testing.T) {
	d, _ := New(nil)
	capture := d.StartRawHapticsCapture()
	d.SetInterfaceAltSetting(InterfaceHapticsAudio, 1)
	generation := d.IsoOutGeneration(EndpointHapticsAudioOut)
	d.handleHapticsAudioOutGeneration(make([]byte, 48*8), generation)
	d.StopRawHapticsCapture()
	d.handleHapticsAudioOutGeneration(make([]byte, 48*8), generation)
	if _, ok := capture.Next(); ok {
		t.Fatal("stopped capture retained or accepted data")
	}
	resumed := d.StartRawHapticsCapture()
	d.handleHapticsAudioOutGeneration(make([]byte, 48*8), generation)
	block, ok := resumed.Next()
	if !ok || block.FirstFrame != 0 || block.Generation != generation {
		t.Fatal("restart did not establish a fresh capture")
	}
}

func BenchmarkRawHapticsCaptureCopy(b *testing.B) {
	capture := &RawHapticsCapture{generation: 1}
	var source [48 * 8]byte
	received := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		capture.push(source[:], 1, received)
		capture.Next()
	}
}

func TestRawHapticsCaptureRejectsOldGenerationAndClearsQueuedAudio(t *testing.T) {
	d, _ := New(nil)
	capture := d.StartRawHapticsCapture()
	d.SetInterfaceAltSetting(InterfaceHapticsAudio, 1)
	old := d.IsoOutGeneration(EndpointHapticsAudioOut)
	d.handleHapticsAudioOutGeneration(make([]byte, 48*8), old)
	d.SetInterfaceAltSetting(InterfaceHapticsAudio, 0)
	d.SetInterfaceAltSetting(InterfaceHapticsAudio, 1)
	if d.handleHapticsAudioOutGeneration(make([]byte, 48*8), old) {
		t.Fatal("stale generation accepted")
	}
	if _, ok := capture.Next(); ok {
		t.Fatal("stale audio survived reset")
	}
	if capture.Generation() == old {
		t.Fatal("capture generation did not change")
	}
}

func TestRawHapticsCaptureCountsOverflowWithoutBlockingUSB(t *testing.T) {
	d, _ := New(nil)
	capture := d.StartRawHapticsCapture()
	d.SetInterfaceAltSetting(InterfaceHapticsAudio, 1)
	generation := d.IsoOutGeneration(EndpointHapticsAudioOut)
	for i := 0; i < 17; i++ {
		d.handleHapticsAudioOutGeneration(make([]byte, 48*8), generation)
	}
	if capture.DroppedFrames() != 48 {
		t.Fatalf("dropped=%d, want 48", capture.DroppedFrames())
	}
	for i := 0; i < 16; i++ {
		if _, ok := capture.Next(); !ok {
			t.Fatal("missing queued block")
		}
	}
	d.handleHapticsAudioOutGeneration(make([]byte, 8), generation)
	block, ok := capture.Next()
	if !ok || block.FirstFrame != 17*48 {
		t.Fatal("overflow gap was hidden from capture")
	}
}
