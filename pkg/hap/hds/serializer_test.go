package hds

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarshal_Values(t *testing.T) {
	tests := []struct {
		value any
		data  []byte
	}{
		{nil, []byte{0x04}},
		{true, []byte{0x01}},
		{false, []byte{0x02}},
		{int64(-1), []byte{0x07}},
		{int64(0), []byte{0x08}},
		{int64(38), []byte{0x2E}},
		{int64(39), []byte{0x30, 39}},
		{int64(-2), []byte{0x30, 0xFE}},
		{int64(1000), []byte{0x31, 0xE8, 0x03}},
		{int64(100000), []byte{0x32, 0xA0, 0x86, 0x01, 0x00}},
		{int64(0x1_0000_0000), []byte{0x33, 0, 0, 0, 0, 1, 0, 0, 0}},
		{float64(1), []byte{0x36, 0, 0, 0, 0, 0, 0, 0xF0, 0x3F}},
		{"", []byte{0x40}},
		{"hello", []byte{0x45, 'h', 'e', 'l', 'l', 'o'}},
		{[]byte{1, 2, 3}, []byte{0x73, 1, 2, 3}},
		{[]any{}, []byte{0xD0}},
		{map[string]any{}, []byte{0xE0}},
	}

	for _, test := range tests {
		data, err := Marshal(test.value)
		require.NoError(t, err)
		require.Equal(t, test.data, data, "value: %v", test.value)

		value, err := Unmarshal(test.data)
		require.NoError(t, err)
		require.EqualValues(t, test.value, value, "data: %x", test.data)
	}
}

func TestMarshal_RoundTrip(t *testing.T) {
	value := map[string]any{
		"protocol": "dataSend",
		"event":    "data",
		"message": map[string]any{
			"streamId": int64(1),
			"packets": []any{
				map[string]any{
					"data": bytes.Repeat([]byte{0xAA}, 1000),
					"metadata": map[string]any{
						"dataType":                "mediaFragment",
						"dataSequenceNumber":      int64(2),
						"dataChunkSequenceNumber": int64(1),
						"isLastDataChunk":         true,
						"dataTotalSize":           int64(262144),
					},
				},
			},
			"endOfStream": false,
			"list":        []any{int64(-100), float64(2.5), nil, "x"},
		},
	}

	data, err := Marshal(value)
	require.NoError(t, err)

	decoded, err := Unmarshal(data)
	require.NoError(t, err)
	require.EqualValues(t, value, decoded)
}

func TestUnmarshal_Header(t *testing.T) {
	// {"protocol":"control","request":"hello","id":1} encoded by a controller
	data := []byte{0xE3}
	data = append(data, 0x48)
	data = append(data, "protocol"...)
	data = append(data, 0x47)
	data = append(data, "control"...)
	data = append(data, 0x47)
	data = append(data, "request"...)
	data = append(data, 0x45)
	data = append(data, "hello"...)
	data = append(data, 0x42)
	data = append(data, "id"...)
	data = append(data, 0x09)

	value, err := Unmarshal(data)
	require.NoError(t, err)
	require.EqualValues(t, map[string]any{
		"protocol": "control",
		"request":  "hello",
		"id":       int64(1),
	}, value)
}

func TestUnmarshal_Compression(t *testing.T) {
	// {"a":"repeat","b":"repeat"} where the second "repeat" is a
	// back-reference to the first one (compression tag)
	data := []byte{0xE2}
	data = append(data, 0x41, 'a')
	data = append(data, 0x46)
	data = append(data, "repeat"...) // tracked: "a"=0, "repeat"=1
	data = append(data, 0x41, 'b')
	data = append(data, 0xA0+1) // back-reference to "repeat"

	value, err := Unmarshal(data)
	require.NoError(t, err)
	require.EqualValues(t, map[string]any{"a": "repeat", "b": "repeat"}, value)
}

func TestMarshal_Message(t *testing.T) {
	// response header + empty message payload framing
	head, err := Marshal(map[string]any{"protocol": "control", "response": "hello", "id": int64(1), "status": int64(0)})
	require.NoError(t, err)

	body, err := Marshal(map[string]any{})
	require.NoError(t, err)

	payload := append([]byte{byte(len(head))}, head...)
	payload = append(payload, body...)

	msg, err := unmarshalMessage(payload)
	require.NoError(t, err)
	require.Equal(t, TypeResponse, msg.Type)
	require.Equal(t, ProtoControl, msg.Protocol)
	require.Equal(t, TopicHello, msg.Topic)
	require.Equal(t, int64(1), msg.ID)
	require.Equal(t, int64(0), msg.Status)
}
