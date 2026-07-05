package mp4

import (
	"encoding/binary"

	"github.com/AlexxIT/go2rtc/pkg/iso"
)

// FragmentSample - a single sample (video frame or audio frame)
// inside a movie fragment.
type FragmentSample struct {
	Duration uint32 // in track timescale units
	Flags    uint32 // iso.SampleVideoIFrame, iso.SampleVideoNonIFrame, iso.SampleAudio
	CTS      uint32 // composition time offset (0 for audio and B-frameless video)
	Data     []byte
}

// FragmentTrack - all samples of a single track inside a movie fragment.
type FragmentTrack struct {
	ID      uint32 // track ID (1-based, same order as in the init segment)
	DTS     uint64 // base media decode time in track timescale units
	Samples []FragmentSample
}

// MarshalFragment builds a single moof+mdat pair with multiple samples
// per track (unlike Muxer.GetPayload which is single-sample). This layout
// matches typical fMP4 recordings (ex. ffmpeg with frag_keyframe) and is
// used for HomeKit Secure Video fragments.
func MarshalFragment(seq uint32, tracks []FragmentTrack) []byte {
	size := 1024
	for i := range tracks {
		for j := range tracks[i].Samples {
			size += len(tracks[i].Samples[j].Data) + 16
		}
	}

	mv := iso.NewMovie(size)

	mv.StartAtom(iso.Moof)

	mv.StartAtom(iso.MoofMfhd)
	mv.Skip(1)          // version
	mv.Skip(3)          // flags
	mv.WriteUint32(seq) // sequence number
	mv.EndAtom()

	// data offsets in trun boxes are relative to the moof start,
	// so they can be calculated only after the moof is complete
	offsets := make([]int, len(tracks))

	for i := range tracks {
		track := &tracks[i]

		useCTS := false
		for j := range track.Samples {
			if track.Samples[j].CTS != 0 {
				useCTS = true
				break
			}
		}

		mv.StartAtom(iso.MoofTraf)

		mv.StartAtom(iso.MoofTrafTfhd)
		mv.Skip(1) // version
		mv.WriteUint24(iso.TfhdDefaultBaseIsMoof)
		mv.WriteUint32(track.ID)
		mv.EndAtom()

		mv.StartAtom(iso.MoofTrafTfdt)
		mv.WriteBytes(1) // version
		mv.Skip(3)       // flags
		mv.WriteUint64(track.DTS)
		mv.EndAtom()

		mv.StartAtom(iso.MoofTrafTrun)
		mv.Skip(1) // version
		flags := uint32(iso.TrunDataOffset | iso.TrunSampleDuration | iso.TrunSampleSize | iso.TrunSampleFlags)
		if useCTS {
			flags |= iso.TrunSampleCTS
		}
		mv.WriteUint24(flags)
		mv.WriteUint32(uint32(len(track.Samples)))

		offsets[i] = len(mv.Bytes()) // data offset position for patching
		mv.WriteUint32(0)            // data offset placeholder

		for j := range track.Samples {
			sample := &track.Samples[j]
			mv.WriteUint32(sample.Duration)
			mv.WriteUint32(uint32(len(sample.Data)))
			mv.WriteUint32(sample.Flags)
			if useCTS {
				mv.WriteUint32(sample.CTS)
			}
		}
		mv.EndAtom() // TRUN

		mv.EndAtom() // TRAF
	}

	mv.EndAtom() // MOOF

	moofSize := len(mv.Bytes())

	// patch data offsets: moof size + mdat header size + preceding tracks data
	dataOffset := moofSize + 8
	for i := range tracks {
		binary.BigEndian.PutUint32(mv.Bytes()[offsets[i]:], uint32(dataOffset))
		for j := range tracks[i].Samples {
			dataOffset += len(tracks[i].Samples[j].Data)
		}
	}

	mv.StartAtom(iso.Mdat)
	for i := range tracks {
		for j := range tracks[i].Samples {
			mv.Write(tracks[i].Samples[j].Data)
		}
	}
	mv.EndAtom()

	return mv.Bytes()
}
