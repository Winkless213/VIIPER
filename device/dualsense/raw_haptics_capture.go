package dualsense

import (
	"sync"
	"time"
)

const RawHapticsBlockFrames = 48

// RawHapticsBlock 保留 48kHz S16LE 左右触觉；不包含前两路扬声器。
type RawHapticsBlock struct {
	Generation uint64
	FirstFrame uint64
	ReceivedAt time.Time
	Frames     int
	PCM        [RawHapticsBlockFrames * 4]byte
}

type RawHapticsCapture struct {
	mu                                   sync.Mutex
	blocks                               [16]RawHapticsBlock
	head, count                          int
	generation, nextFrame, droppedFrames uint64
}

func (d *DualSense) StartRawHapticsCapture() *RawHapticsCapture {
	d.mediaMu.Lock()
	defer d.mediaMu.Unlock()
	if d.rawHapticsCapture == nil {
		d.rawHapticsCapture = &RawHapticsCapture{generation: d.speakerMediaGeneration}
	}
	return d.rawHapticsCapture
}

func (d *DualSense) StopRawHapticsCapture() {
	d.mediaMu.Lock()
	defer d.mediaMu.Unlock()
	if d.rawHapticsCapture != nil {
		d.rawHapticsCapture.reset(0)
	}
	d.rawHapticsCapture = nil
}

func (c *RawHapticsCapture) reset(generation uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.head, c.count, c.nextFrame, c.generation = 0, 0, 0, generation
}

// 输入线程只复制固定上限的数据；磁盘和网络工作由消费者承担。
func (c *RawHapticsCapture) push(pcm []byte, generation uint64, received time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(pcm) >= USBHapticsAudioFrameSize {
		frames := min(len(pcm)/USBHapticsAudioFrameSize, RawHapticsBlockFrames)
		if c.count == len(c.blocks) {
			c.droppedFrames += uint64(frames)
		} else {
			block := &c.blocks[(c.head+c.count)%len(c.blocks)]
			block.Generation, block.FirstFrame, block.ReceivedAt, block.Frames = generation, c.nextFrame, received, frames
			for i := 0; i < frames; i++ {
				copy(block.PCM[i*4:i*4+4], pcm[i*8+4:i*8+8])
			}
			c.count++
		}
		c.nextFrame += uint64(frames)
		pcm = pcm[frames*USBHapticsAudioFrameSize:]
	}
}

func (c *RawHapticsCapture) Next() (RawHapticsBlock, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.count == 0 {
		return RawHapticsBlock{}, false
	}
	block := c.blocks[c.head]
	c.head = (c.head + 1) % len(c.blocks)
	c.count--
	return block, true
}

func (c *RawHapticsCapture) Generation() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

func (c *RawHapticsCapture) DroppedFrames() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.droppedFrames
}
