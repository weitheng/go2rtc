package camera

import (
	"github.com/AlexxIT/go2rtc/pkg/hap"
	"github.com/AlexxIT/go2rtc/pkg/hap/tlv8"
)

//goland:noinspection ALL
const (
	// services
	TypeServiceMotionSensor                  = "85"
	TypeServiceCameraRecordingManagement     = "204"
	TypeServiceCameraOperatingMode           = "21A"
	TypeServiceDataStreamTransportManagement = "129"

	// characteristics
	TypeMotionDetected          = "22"
	TypeStatusActive            = "75"
	TypeActive                  = "B0"
	TypeVersion                 = "37"
	TypeRecordingAudioActive    = "226"
	TypeEventSnapshotsActive    = "223"
	TypePeriodicSnapshotsActive = "225"
	TypeHomeKitCameraActive     = "21B"
)

// RecordingOptions - HKSV recording capabilities of the accessory.
type RecordingOptions struct {
	PrebufferLength uint32 // ms, default 4000
	FragmentLength  uint32 // ms, default 4000
}

// SecureVideoServices returns HAP services required for HomeKit Secure Video:
// MotionSensor, CameraRecordingManagement, CameraOperatingMode and
// DataStreamTransportManagement.
//
// Important: the accessory IIDs should be initialized (Accessory.InitIID)
// after appending these services.
func SecureVideoServices(opts RecordingOptions) ([]*hap.Service, error) {
	if opts.PrebufferLength == 0 {
		opts.PrebufferLength = 4000
	}
	if opts.FragmentLength == 0 {
		opts.FragmentLength = 4000
	}

	val205, err := tlv8.MarshalBase64(SupportedCameraRecordingConfiguration{
		PrebufferLength:     opts.PrebufferLength,
		EventTriggerOptions: EventTriggerMotion,
		MediaContainerConfigs: []MediaContainerConfiguration{
			{
				MediaContainerType: MediaContainerTypeFragmentedMP4,
				MediaContainerParams: MediaContainerParameters{
					FragmentLength: opts.FragmentLength,
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	val206, err := tlv8.MarshalBase64(SupportedVideoRecordingConfiguration{
		CodecConfigs: []VideoRecordingCodecConfiguration{
			{
				CodecType: VideoCodecTypeH264,
				CodecParams: VideoRecordingCodecParameters{
					ProfileID: []byte{
						VideoCodecProfileConstrainedBaseline,
						VideoCodecProfileMain,
						VideoCodecProfileHigh,
					},
					Level: []byte{
						VideoCodecLevel31,
						VideoCodecLevel32,
						VideoCodecLevel40,
					},
				},
				VideoAttrs: []VideoCodecAttributes{
					{Width: 1920, Height: 1080, Framerate: 30},
					{Width: 1920, Height: 1080, Framerate: 15},
					{Width: 1280, Height: 720, Framerate: 30},
					{Width: 1280, Height: 720, Framerate: 15},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	// audio configuration is required by the spec even for video-only
	// cameras, the hub respects the RecordingAudioActive characteristic
	val207, err := tlv8.MarshalBase64(SupportedAudioRecordingConfiguration{
		CodecConfigs: []AudioRecordingCodecConfiguration{
			{
				CodecType: AudioRecordingCodecTypeAACLC,
				CodecParams: AudioRecordingCodecParameters{
					Channels:    1,
					BitrateMode: []byte{AudioCodecBitrateVariable},
					SampleRate: []byte{
						AudioRecordingSampleRate8Khz,
						AudioRecordingSampleRate16Khz,
						AudioRecordingSampleRate24Khz,
						AudioRecordingSampleRate32Khz,
						AudioRecordingSampleRate44Khz,
						AudioRecordingSampleRate48Khz,
					},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	val130, err := tlv8.MarshalBase64(SupportedDataStreamTransportConfiguration{
		Configs: []TransferTransportConfiguration{
			{TransportType: 0}, // 0 - HomeKit Data Stream over TCP
		},
	})
	if err != nil {
		return nil, err
	}

	services := []*hap.Service{
		{
			Type: TypeServiceMotionSensor,
			Characters: []*hap.Character{
				{
					Type:   TypeMotionDetected,
					Format: hap.FormatBool,
					Value:  false,
					Perms:  hap.EVPR,
					//Descr:  "Motion Detected",
				},
				{
					Type:   TypeStatusActive,
					Format: hap.FormatBool,
					Value:  true,
					Perms:  hap.EVPR,
					//Descr:  "Status Active",
				},
			},
		},
		{
			Type: TypeServiceCameraRecordingManagement,
			Characters: []*hap.Character{
				{
					Type:   TypeActive,
					Format: hap.FormatUInt8,
					Value:  0,
					Perms:  hap.EVPRPW,
					//Descr:  "Active",
				},
				{
					Type:   TypeSupportedCameraRecordingConfiguration,
					Format: hap.FormatTLV8,
					Value:  val205,
					Perms:  hap.PR,
					//Descr:  "Supported Camera Recording Configuration",
				},
				{
					Type:   TypeSupportedVideoRecordingConfiguration,
					Format: hap.FormatTLV8,
					Value:  val206,
					Perms:  hap.PR,
					//Descr:  "Supported Video Recording Configuration",
				},
				{
					Type:   TypeSupportedAudioRecordingConfiguration,
					Format: hap.FormatTLV8,
					Value:  val207,
					Perms:  hap.PR,
					//Descr:  "Supported Audio Recording Configuration",
				},
				{
					Type:   TypeSelectedCameraRecordingConfiguration,
					Format: hap.FormatTLV8,
					Value:  "", // important empty
					Perms:  hap.PRPW,
					//Descr:  "Selected Camera Recording Configuration",
				},
				{
					Type:   TypeRecordingAudioActive,
					Format: hap.FormatUInt8,
					Value:  0,
					Perms:  hap.EVPRPW,
					//Descr:  "Recording Audio Active",
				},
			},
		},
		{
			Type: TypeServiceCameraOperatingMode,
			Characters: []*hap.Character{
				{
					Type:   TypeEventSnapshotsActive,
					Format: hap.FormatUInt8,
					Value:  1,
					Perms:  hap.EVPRPW,
					//Descr:  "Event Snapshots Active",
				},
				{
					Type:   TypeHomeKitCameraActive,
					Format: hap.FormatUInt8,
					Value:  1,
					Perms:  hap.EVPRPW,
					//Descr:  "HomeKit Camera Active",
				},
				{
					Type:   TypePeriodicSnapshotsActive,
					Format: hap.FormatUInt8,
					Value:  1,
					Perms:  hap.EVPRPW,
					//Descr:  "Periodic Snapshots Active",
				},
			},
		},
		{
			Type: TypeServiceDataStreamTransportManagement,
			Characters: []*hap.Character{
				{
					Type:   TypeSupportedDataStreamTransportConfiguration,
					Format: hap.FormatTLV8,
					Value:  val130,
					Perms:  hap.PR,
					//Descr:  "Supported Data Stream Transport Configuration",
				},
				{
					Type:   TypeSetupDataStreamTransport,
					Format: hap.FormatTLV8,
					Value:  "", // important empty
					Perms:  hap.PRPWWR,
					//Descr:  "Setup Data Stream Transport",
				},
				{
					Type:   TypeVersion,
					Format: hap.FormatString,
					Value:  "1.0",
					Perms:  hap.PR,
					//Descr:  "Version",
				},
			},
		},
	}

	return services, nil
}
