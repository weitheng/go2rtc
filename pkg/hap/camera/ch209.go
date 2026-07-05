package camera

const TypeSelectedCameraRecordingConfiguration = "209"

// SelectedCameraRecordingConfiguration - recording configuration, written
// by the controller (Apple Home hub) after it reads the supported
// configurations. Same TLV tags as the supported structs, but all values
// are scalars.
type SelectedCameraRecordingConfiguration struct {
	GeneralConfig SelectedGeneralConfiguration `tlv8:"1"`
	VideoConfig   SelectedVideoConfiguration   `tlv8:"2"`
	AudioConfig   SelectedAudioConfiguration   `tlv8:"3"`
}

type SelectedGeneralConfiguration struct {
	PrebufferLength      uint32                      `tlv8:"1"` // ms
	EventTriggerOptions  uint64                      `tlv8:"2"` // bitmask
	MediaContainerConfig MediaContainerConfiguration `tlv8:"3"`
}

type SelectedVideoConfiguration struct {
	CodecType   uint8                   `tlv8:"1"` // 0 - H.264
	CodecParams SelectedVideoParameters `tlv8:"2"`
	VideoAttrs  VideoCodecAttributes    `tlv8:"3"`
}

type SelectedVideoParameters struct {
	ProfileID      uint8  `tlv8:"1"` // 0 - baseline, 1 - main, 2 - high
	Level          uint8  `tlv8:"2"` // 0 - 3.1, 1 - 3.2, 2 - 4.0
	Bitrate        uint32 `tlv8:"3"` // kbps
	IFrameInterval uint32 `tlv8:"4"` // ms
}

type SelectedAudioConfiguration struct {
	CodecType   uint8                   `tlv8:"1"` // 0 - AAC-LC, 1 - AAC-ELD
	CodecParams SelectedAudioParameters `tlv8:"2"`
}

type SelectedAudioParameters struct {
	Channels        uint8  `tlv8:"1"`
	BitrateMode     uint8  `tlv8:"2"` // 0 - variable, 1 - constant
	SampleRate      uint8  `tlv8:"3"` // enum, see AudioRecordingSampleRates
	MaxAudioBitrate uint32 `tlv8:"4"` // kbps
}
