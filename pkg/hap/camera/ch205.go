package camera

const TypeSupportedCameraRecordingConfiguration = "205"

//goland:noinspection ALL
const (
	EventTriggerMotion   = 1
	EventTriggerDoorbell = 2

	MediaContainerTypeFragmentedMP4 = 0
)

type SupportedCameraRecordingConfiguration struct {
	PrebufferLength       uint32                        `tlv8:"1"` // ms
	EventTriggerOptions   uint64                        `tlv8:"2"` // bitmask
	MediaContainerConfigs []MediaContainerConfiguration `tlv8:"3"`
}

type MediaContainerConfiguration struct {
	MediaContainerType   uint8                    `tlv8:"1"`
	MediaContainerParams MediaContainerParameters `tlv8:"2"`
}

type MediaContainerParameters struct {
	FragmentLength uint32 `tlv8:"1"` // ms
}
