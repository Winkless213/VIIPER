package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Alia5/VIIPER/device/dualsense"
	"github.com/Alia5/VIIPER/internal/server/usb"
	"github.com/Alia5/VIIPER/virtualbus"
)

type audioMessage struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Generation  uint64 `json:"generation"`
	FirstFrame  uint64 `json:"first_frame"`
	ReceivedNS  int64  `json:"received_ns"`
	HostQueueUS int64  `json:"host_queue_us"`
	SampleRate  int    `json:"sample_rate"`
	Frames      int    `json:"frames"`
	PCM         []byte `json:"pcm"`
}

func audio(block dualsense.RawHapticsBlock, source string) audioMessage {
	return audioMessage{"audio", source, block.Generation, block.FirstFrame,
		block.ReceivedAt.UnixNano(), time.Since(block.ReceivedAt).Microseconds(),
		48000, block.Frames, block.PCM[:block.Frames*4]}
}

func syntheticCapture(output io.Writer) error {
	d, err := dualsense.New(nil)
	if err != nil {
		return err
	}
	capture := d.StartRawHapticsCapture()
	defer d.StopRawHapticsCapture()
	d.SetInterfaceAltSetting(dualsense.InterfaceHapticsAudio, 1)
	data := make([]byte, 48*8)
	for i := 0; i < 48; i++ {
		binary.LittleEndian.PutUint16(data[i*8:], 30000)
		binary.LittleEndian.PutUint16(data[i*8+2:], 20000)
		binary.LittleEndian.PutUint16(data[i*8+4:], uint16(1000+i))
		binary.LittleEndian.PutUint16(data[i*8+6:], uint16(int16(-1000-i)))
	}
	d.HandleTransfer(context.Background(), dualsense.EndpointHapticsAudioOut, 0, data)
	block, ok := capture.Next()
	if !ok {
		return fmt.Errorf("synthetic capture missing")
	}
	return json.NewEncoder(output).Encode(audio(block, "synthetic"))
}

type inputCommand struct {
	Type  string               `json:"type"`
	State dualsense.InputState `json:"state"`
}

func readInputs(ctx context.Context, d *dualsense.DualSense, last *atomic.Int64, stop context.CancelFunc) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var command inputCommand
		if json.Unmarshal(scanner.Bytes(), &command) != nil {
			continue
		}
		if command.Type == "quit" {
			stop()
			return
		}
		if command.Type == "input" && ctx.Err() == nil {
			d.UpdateInputState(&command.State)
			last.Store(time.Now().UnixNano())
		}
	}
	stop()
}

func serve(duration time.Duration, address string) error {
	d, err := dualsense.New(nil)
	if err != nil {
		return err
	}
	capture := d.StartRawHapticsCapture()
	defer d.StopRawHapticsCapture()
	bus, err := virtualbus.NewWithBusID(1)
	if err != nil {
		return err
	}
	defer bus.Close()
	registration, err := bus.AddRegistration(d)
	if err != nil {
		return err
	}
	server := usb.New(usb.ServerConfig{Addr: address, ConnectionTimeout: 30 * time.Second,
		WriteBatchFlushInterval: time.Millisecond}, slog.Default(), nil)
	if err := server.AddBus(bus); err != nil {
		return err
	}
	defer server.Close()
	serverError := make(chan error, 1)
	go func() { serverError <- server.ListenAndServe() }()
	select {
	case <-server.Ready():
	case err := <-serverError:
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(map[string]any{"type": "ready", "address": server.Addr(),
		"busid":           strings.TrimRight(string(registration.Meta.USBBusID[:]), "\x00"),
		"hardware_output": false, "attached_to_windows": false}); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, duration)
	defer timeout()
	var lastInput, controlDrops atomic.Int64
	controls := make(chan [dualsense.OutputReportSize]byte, 64)
	d.SetOutputCallback(func(state dualsense.OutputState) {
		select {
		case controls <- state.RawOutputReport:
		default:
			controlDrops.Add(1)
		}
	})
	defer d.SetOutputCallback(nil)
	go readInputs(ctx, d, &lastInput, cancel)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	var lastGeneration uint64
	for {
		select {
		case <-ctx.Done():
			d.UpdateInputState(dualsense.NewInputState())
			return encoder.Encode(map[string]any{"type": "stopped", "dropped_frames": capture.DroppedFrames(), "control_drops": controlDrops.Load()})
		case err := <-serverError:
			return err
		case report := <-controls:
			if err := encoder.Encode(map[string]any{"type": "output", "source": "usb-host", "report": report[:]}); err != nil {
				return err
			}
		case <-tick.C:
			if time.Now().UnixNano()-lastInput.Load() > int64(250*time.Millisecond) {
				d.UpdateInputState(dualsense.NewInputState())
			}
			generation := capture.Generation()
			if generation != lastGeneration {
				lastGeneration = generation
				if err := encoder.Encode(map[string]any{"type": "reset", "generation": generation}); err != nil {
					return err
				}
			}
			for {
				block, ok := capture.Next()
				if !ok {
					break
				}
				if block.Generation != capture.Generation() {
					continue
				}
				if err := encoder.Encode(audio(block, "usb-host")); err != nil {
					return err
				}
			}
		}
	}
}

func main() {
	selfTest := flag.Bool("self-test", false, "Synthetic input only; no server or hardware output")
	duration := flag.Duration("duration", time.Minute, "Maximum capture time")
	port := flag.Uint("port", 3241, "Loopback USB/IP port")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	var err error
	if *selfTest {
		err = syntheticCapture(os.Stdout)
	} else if *port > 65535 || *duration <= 0 {
		err = fmt.Errorf("invalid port or duration")
	} else {
		err = serve(*duration, fmt.Sprintf("127.0.0.1:%d", *port))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
