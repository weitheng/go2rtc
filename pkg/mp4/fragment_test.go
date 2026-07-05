package mp4

import (
	"encoding/binary"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/iso"
	"github.com/stretchr/testify/require"
)

func readAtom(t *testing.T, b []byte) (name string, payload, rest []byte) {
	require.GreaterOrEqual(t, len(b), 8)
	size := binary.BigEndian.Uint32(b)
	require.GreaterOrEqual(t, int(size), 8)
	require.LessOrEqual(t, int(size), len(b))
	return string(b[4:8]), b[8:size], b[size:]
}

func TestMarshalFragment(t *testing.T) {
	video1 := []byte{0x01, 0x02, 0x03, 0x04}
	video2 := []byte{0x05, 0x06, 0x07}
	audio1 := []byte{0x08, 0x09}

	tracks := []FragmentTrack{
		{
			ID:  1,
			DTS: 90000,
			Samples: []FragmentSample{
				{Duration: 3000, Flags: iso.SampleVideoIFrame, Data: video1},
				{Duration: 3000, Flags: iso.SampleVideoNonIFrame, CTS: 500, Data: video2},
			},
		},
		{
			ID:  2,
			DTS: 1024,
			Samples: []FragmentSample{
				{Duration: 1024, Flags: iso.SampleAudio, Data: audio1},
			},
		},
	}

	b := MarshalFragment(7, tracks)

	// top-level: moof + mdat
	name, moof, rest := readAtom(t, b)
	require.Equal(t, "moof", name)
	moofSize := 8 + len(moof)

	name, mdat, rest := readAtom(t, rest)
	require.Equal(t, "mdat", name)
	require.Empty(t, rest)

	// mdat contains all samples in track order
	require.Equal(t, append(append(append([]byte(nil), video1...), video2...), audio1...), mdat)

	// moof: mfhd + traf + traf
	name, mfhd, moofRest := readAtom(t, moof)
	require.Equal(t, "mfhd", name)
	require.Equal(t, uint32(7), binary.BigEndian.Uint32(mfhd[4:]))

	name, traf1, moofRest := readAtom(t, moofRest)
	require.Equal(t, "traf", name)

	name, traf2, moofRest := readAtom(t, moofRest)
	require.Equal(t, "traf", name)
	require.Empty(t, moofRest)

	checkTraf := func(traf []byte, trackID uint32, dts uint64, samples []FragmentSample, useCTS bool, dataOffset int) {
		name, tfhd, trafRest := readAtom(t, traf)
		require.Equal(t, "tfhd", name)
		require.Equal(t, uint32(iso.TfhdDefaultBaseIsMoof), binary.BigEndian.Uint32(tfhd[0:])&0xFFFFFF)
		require.Equal(t, trackID, binary.BigEndian.Uint32(tfhd[4:]))

		name, tfdt, trafRest := readAtom(t, trafRest)
		require.Equal(t, "tfdt", name)
		require.Equal(t, dts, binary.BigEndian.Uint64(tfdt[4:]))

		name, trun, trafRest := readAtom(t, trafRest)
		require.Equal(t, "trun", name)
		require.Empty(t, trafRest)

		flags := binary.BigEndian.Uint32(trun[0:]) & 0xFFFFFF
		expected := uint32(iso.TrunDataOffset | iso.TrunSampleDuration | iso.TrunSampleSize | iso.TrunSampleFlags)
		if useCTS {
			expected |= iso.TrunSampleCTS
		}
		require.Equal(t, expected, flags)
		require.Equal(t, uint32(len(samples)), binary.BigEndian.Uint32(trun[4:]))
		require.Equal(t, uint32(dataOffset), binary.BigEndian.Uint32(trun[8:]))

		entry := trun[12:]
		for _, sample := range samples {
			require.Equal(t, sample.Duration, binary.BigEndian.Uint32(entry[0:]))
			require.Equal(t, uint32(len(sample.Data)), binary.BigEndian.Uint32(entry[4:]))
			require.Equal(t, sample.Flags, binary.BigEndian.Uint32(entry[8:]))
			if useCTS {
				require.Equal(t, sample.CTS, binary.BigEndian.Uint32(entry[12:]))
				entry = entry[16:]
			} else {
				entry = entry[12:]
			}
		}
		require.Empty(t, entry)
	}

	// video track uses CTS (sample 2 has non zero CTS)
	checkTraf(traf1, 1, 90000, tracks[0].Samples, true, moofSize+8)
	// audio track data starts after all video samples
	checkTraf(traf2, 2, 1024, tracks[1].Samples, false, moofSize+8+len(video1)+len(video2))
}
