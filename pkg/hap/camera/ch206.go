package camera

const TypeSupportedVideoRecordingConfiguration = "206"

type SupportedVideoRecordingConfiguration struct {
	CodecConfigs []VideoRecordingCodecConfiguration `tlv8:"1"`
}

type VideoRecordingCodecConfiguration struct {
	CodecType   uint8                         `tlv8:"1"` // 0 - H.264
	CodecParams VideoRecordingCodecParameters `tlv8:"2"`
	VideoAttrs  []VideoCodecAttributes        `tlv8:"3"`
}

type VideoRecordingCodecParameters struct {
	ProfileID []byte `tlv8:"1"` // 0 - baseline, 1 - main, 2 - high
	Level     []byte `tlv8:"2"` // 0 - 3.1, 1 - 3.2, 2 - 4.0
}
