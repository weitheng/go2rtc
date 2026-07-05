package camera

import (
	"encoding/json"
	"testing"

	"github.com/AlexxIT/go2rtc/pkg/hap"
	"github.com/AlexxIT/go2rtc/pkg/hap/tlv8"
	"github.com/stretchr/testify/require"
)

func TestSelectedRecordingConfiguration(t *testing.T) {
	src := SelectedCameraRecordingConfiguration{
		GeneralConfig: SelectedGeneralConfiguration{
			PrebufferLength:     4000,
			EventTriggerOptions: EventTriggerMotion,
			MediaContainerConfig: MediaContainerConfiguration{
				MediaContainerType: MediaContainerTypeFragmentedMP4,
				MediaContainerParams: MediaContainerParameters{
					FragmentLength: 4000,
				},
			},
		},
		VideoConfig: SelectedVideoConfiguration{
			CodecType: VideoCodecTypeH264,
			CodecParams: SelectedVideoParameters{
				ProfileID:      VideoCodecProfileMain,
				Level:          VideoCodecLevel40,
				Bitrate:        2000,
				IFrameInterval: 4000,
			},
			VideoAttrs: VideoCodecAttributes{
				Width: 1920, Height: 1080, Framerate: 30,
			},
		},
		AudioConfig: SelectedAudioConfiguration{
			CodecType: AudioRecordingCodecTypeAACLC,
			CodecParams: SelectedAudioParameters{
				Channels:        1,
				BitrateMode:     AudioCodecBitrateVariable,
				SampleRate:      AudioRecordingSampleRate32Khz,
				MaxAudioBitrate: 48,
			},
		},
	}

	value, err := tlv8.MarshalBase64(src)
	require.NoError(t, err)

	// same way as Apple Home hub writes the characteristic
	var dst SelectedCameraRecordingConfiguration
	err = tlv8.UnmarshalBase64(value, &dst)
	require.NoError(t, err)
	require.Equal(t, src, dst)
}

func TestSecureVideoServices(t *testing.T) {
	acc := NewAccessory("AlexxIT", "go2rtc", "Camera", "-", "1.0.0")

	services, err := SecureVideoServices(RecordingOptions{})
	require.NoError(t, err)
	require.Len(t, services, 4)

	acc.Services = append(acc.Services, services...)
	acc.InitIID()

	// all services and characteristics should be resolvable
	for _, serviceType := range []string{
		TypeServiceMotionSensor,
		TypeServiceCameraRecordingManagement,
		TypeServiceCameraOperatingMode,
		TypeServiceDataStreamTransportManagement,
	} {
		service := acc.GetService(serviceType)
		require.NotNil(t, service, serviceType)
		require.NotZero(t, service.IID)

		for _, char := range service.Characters {
			require.NotZero(t, char.IID)
			require.Equal(t, char, acc.GetCharacterByID(char.IID))
			require.Equal(t, service, acc.GetServiceByCharacterIID(char.IID))
		}
	}

	// supported configurations should be valid TLV8
	rm := acc.GetService(TypeServiceCameraRecordingManagement)

	var v205 SupportedCameraRecordingConfiguration
	err = rm.GetCharacter(TypeSupportedCameraRecordingConfiguration).ReadTLV8(&v205)
	require.NoError(t, err)
	require.Equal(t, uint32(4000), v205.PrebufferLength)
	require.Equal(t, uint64(EventTriggerMotion), v205.EventTriggerOptions)
	require.Len(t, v205.MediaContainerConfigs, 1)
	require.Equal(t, uint32(4000), v205.MediaContainerConfigs[0].MediaContainerParams.FragmentLength)

	var v206 SupportedVideoRecordingConfiguration
	err = rm.GetCharacter(TypeSupportedVideoRecordingConfiguration).ReadTLV8(&v206)
	require.NoError(t, err)
	require.Len(t, v206.CodecConfigs, 1)
	require.Len(t, v206.CodecConfigs[0].VideoAttrs, 4)

	var v207 SupportedAudioRecordingConfiguration
	err = rm.GetCharacter(TypeSupportedAudioRecordingConfiguration).ReadTLV8(&v207)
	require.NoError(t, err)
	require.Len(t, v207.CodecConfigs, 1)
	require.EqualValues(t, AudioRecordingCodecTypeAACLC, v207.CodecConfigs[0].CodecType)

	// Active char should be unique per service
	rtp := acc.GetService("110")
	require.NotNil(t, rtp)
	activeRTP := rtp.GetCharacter(TypeActive)
	activeRec := rm.GetCharacter(TypeActive)
	require.NotNil(t, activeRTP)
	require.NotNil(t, activeRec)
	require.NotEqual(t, activeRTP.IID, activeRec.IID)

	// accessory should serialize to JSON (GET /accessories)
	_, err = json.Marshal(hap.JSONAccessories{Value: []*hap.Accessory{acc}})
	require.NoError(t, err)
}
