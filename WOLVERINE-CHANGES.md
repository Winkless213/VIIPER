# Wolverine changes

This branch modifies VIIPER (GPL-3.0) for the [Wolverine DS5](https://github.com/Winkless213/wolverine-ds5) bridge.
Base: `hbashton/VIIPER@b78e31e93e84b4cb7c4b4c15b8aace10749106d2`. Modified by Winkless213, October 2026.

- `cmd/wolverine-capture/`: a capture program that serves one virtual DualSense over USB/IP on loopback and talks JSON Lines over stdin/stdout. Input states go in; raw haptics PCM and host output reports (triggers, rumble) come out. The input has a 250 ms watchdog that returns to neutral.
- `device/dualsense/raw_haptics_capture.go`: a 16-block ring buffer for the haptics channels of the DualSense USB audio. It counts drops and resets the generation when the audio stream reopens.
- `device/dualsense/device.go`: hooks the capture buffer into the audio path.

Build: `go build -o wolverine-capture.exe ./cmd/wolverine-capture`
