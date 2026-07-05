package homekit

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/stretchr/testify/require"
)

func TestFragmenter(t *testing.T) {
	videoCodec := &core.Codec{Name: core.CodecH264, ClockRate: 90000}

	f := newFragmenter(videoCodec, nil, 4000)

	init, err := f.init()
	require.NoError(t, err)
	require.Equal(t, "ftyp", string(init[4:8]))

	const frameDur = 3000                           // 30 fps
	payload := []byte{0x00, 0x00, 0x00, 0x01, 0x65} // fake IDR

	var fragments [][]byte

	// two GOPs with 4s keyframe interval
	for i := 0; i <= 240; i++ {
		frame := recFrame{
			track:   recTrackVideo,
			key:     i%120 == 0,
			ts:      uint32(i * frameDur),
			payload: payload,
		}
		if fragment := f.push(frame); fragment != nil {
			fragments = append(fragments, fragment)
		}
	}

	require.Len(t, fragments, 2)

	for i, fragment := range fragments {
		moof := atom(t, fragment, "moof")
		mfhd := atom(t, moof, "mfhd")
		require.Equal(t, uint32(i+1), binary.BigEndian.Uint32(mfhd[4:]), "mfhd sequence")

		traf := atom(t, moof[8+len(mfhd):], "traf")
		tfdt := atom(t, traf[8+len(atom(t, traf, "tfhd")):], "tfdt")
		require.Equal(t, uint64(i*120*frameDur), binary.BigEndian.Uint64(tfdt[4:]), "base decode time")

		trun := atom(t, traf[8+len(atom(t, traf, "tfhd"))+8+len(tfdt):], "trun")
		require.Equal(t, uint32(120), binary.BigEndian.Uint32(trun[4:]), "sample count")
	}
}

// atom checks the atom name and returns its payload.
func atom(t *testing.T, b []byte, name string) []byte {
	require.GreaterOrEqual(t, len(b), 8)
	size := binary.BigEndian.Uint32(b)
	require.Equal(t, name, string(b[4:8]))
	require.LessOrEqual(t, int(size), len(b))
	return b[8:size]
}

func TestRecConsumerPrebuffer(t *testing.T) {
	c := newRecConsumer()

	now := time.Now()

	// 20 seconds of video with 4s GOPs, oldest GOPs should be pruned
	for i := 0; i <= 20*30; i++ {
		c.push(recFrame{
			track: recTrackVideo,
			key:   i%120 == 0,
			ts:    uint32(i * 3000),
			wall:  now.Add(time.Duration(i-20*30) * time.Second / 30),
		})
	}

	// buffer should start with a keyframe
	require.NotEmpty(t, c.buf)
	require.True(t, c.buf[0].key)

	// buffer should cover at least recKeepDuration, but not much more
	age := now.Sub(c.buf[0].wall)
	require.GreaterOrEqual(t, age, recKeepDuration)
	require.Less(t, age, recKeepDuration+8*time.Second)

	// subscribe with 4s prebuffer: first frame is a keyframe close to -4s
	frames, live, err := c.subscribe(4 * time.Second)
	require.NoError(t, err)
	require.NotNil(t, live)
	require.True(t, frames[0].key)

	age = now.Sub(frames[0].wall)
	require.GreaterOrEqual(t, age, 4*time.Second)
	require.Less(t, age, 8*time.Second)

	// live frames should arrive on the channel
	_, _, err = c.subscribe(4 * time.Second)
	require.Error(t, err) // second session not allowed

	c.push(recFrame{track: recTrackVideo, ts: 1, wall: now})
	select {
	case frame := <-live:
		require.Equal(t, uint32(1), frame.ts)
	default:
		t.Fatal("no live frame")
	}

	c.unsubscribe()
}
