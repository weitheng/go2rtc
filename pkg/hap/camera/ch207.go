package camera

const TypeSupportedAudioRecordingConfiguration = "207"

//goland:noinspection ALL
const (
	AudioRecordingCodecTypeAACLC  = 0
	AudioRecordingCodecTypeAACELD = 1

	AudioRecordingSampleRate8Khz  = 0
	AudioRecordingSampleRate16Khz = 1
	AudioRecordingSampleRate24Khz = 2
	AudioRecordingSampleRate32Khz = 3
	AudioRecordingSampleRate44Khz = 4
	AudioRecordingSampleRate48Khz = 5
)

// AudioRecordingSampleRates - recording samplerate enum to Hz.
var AudioRecordingSampleRates = map[uint8]uint32{
	AudioRecordingSampleRate8Khz:  8000,
	AudioRecordingSampleRate16Khz: 16000,
	AudioRecordingSampleRate24Khz: 24000,
	AudioRecordingSampleRate32Khz: 32000,
	AudioRecordingSampleRate44Khz: 44100,
	AudioRecordingSampleRate48Khz: 48000,
}

type SupportedAudioRecordingConfiguration struct {
	CodecConfigs []AudioRecordingCodecConfiguration `tlv8:"1"`
}

type AudioRecordingCodecConfiguration struct {
	CodecType   uint8                         `tlv8:"1"` // 0 - AAC-LC, 1 - AAC-ELD
	CodecParams AudioRecordingCodecParameters `tlv8:"2"`
}

type AudioRecordingCodecParameters struct {
	Channels    uint8  `tlv8:"1"`
	BitrateMode []byte `tlv8:"2"` // 0 - variable, 1 - constant
	SampleRate  []byte `tlv8:"3"` // enum, see AudioRecordingSampleRates
}
