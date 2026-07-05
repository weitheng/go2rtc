package hds

import (
	"errors"
	"fmt"
)

// Known HDS protocols and topics.
const (
	ProtoControl  = "control"
	ProtoDataSend = "dataSend"

	TopicHello = "hello"
	TopicOpen  = "open"
	TopicData  = "data"
	TopicAck   = "ack"
	TopicClose = "close"
)

// HDS response status codes.
const (
	StatusSuccess               = 0
	StatusOutOfMemory           = 1
	StatusTimeout               = 2
	StatusHeaderError           = 3
	StatusPayloadError          = 4
	StatusMissingProtocol       = 5
	StatusProtocolSpecificError = 6
)

// Close reasons for the dataSend protocol.
const (
	ReasonNormal               = 0
	ReasonNotAllowed           = 1
	ReasonBusy                 = 2
	ReasonCancelled            = 3
	ReasonUnsupported          = 4
	ReasonUnexpectedFailure    = 5
	ReasonTimeout              = 6
	ReasonBadData              = 7
	ReasonProtocolError        = 8
	ReasonInvalidConfiguration = 9
)

// maxPayloadSize is the max size of a single HDS frame payload (20 bit).
const maxPayloadSize = 0xFFFFF

type MessageType byte

const (
	TypeEvent MessageType = iota + 1
	TypeRequest
	TypeResponse
)

// Message - a single decoded HDS message (event, request or response).
type Message struct {
	Type     MessageType
	Protocol string
	Topic    string
	ID       int64 // for requests and responses
	Status   int64 // for responses
	Message  map[string]any
}

func (m *Message) String() string {
	return fmt.Sprintf("proto=%s topic=%s type=%d id=%d status=%d", m.Protocol, m.Topic, m.Type, m.ID, m.Status)
}

// ReadMessage reads and decodes a single HDS message from the connection.
func (c *Conn) ReadMessage() (*Message, error) {
	b := make([]byte, maxPayloadSize)
	n, err := c.Read(b)
	if err != nil {
		return nil, err
	}
	return unmarshalMessage(b[:n])
}

func unmarshalMessage(b []byte) (*Message, error) {
	if len(b) < 2 {
		return nil, errors.New("hds: message too short")
	}

	headerLen := int(b[0])
	if 1+headerLen > len(b) {
		return nil, errors.New("hds: wrong message header size")
	}

	rawHeader, err := Unmarshal(b[1 : 1+headerLen])
	if err != nil {
		return nil, err
	}
	header, ok := rawHeader.(map[string]any)
	if !ok {
		return nil, errors.New("hds: message header should be dict")
	}

	rawBody, err := Unmarshal(b[1+headerLen:])
	if err != nil {
		return nil, err
	}
	body, ok := rawBody.(map[string]any)
	if !ok {
		return nil, errors.New("hds: message body should be dict")
	}

	msg := &Message{Message: body}
	msg.Protocol, _ = header["protocol"].(string)

	switch {
	case header["event"] != nil:
		msg.Type = TypeEvent
		msg.Topic, _ = header["event"].(string)
	case header["request"] != nil:
		msg.Type = TypeRequest
		msg.Topic, _ = header["request"].(string)
		msg.ID, _ = header["id"].(int64)
	case header["response"] != nil:
		msg.Type = TypeResponse
		msg.Topic, _ = header["response"].(string)
		msg.ID, _ = header["id"].(int64)
		msg.Status, _ = header["status"].(int64)
	default:
		return nil, fmt.Errorf("hds: unknown message header: %v", header)
	}

	return msg, nil
}

// WriteMessage encodes and writes a message with a raw header dict.
// Writes are serialized, so it is safe to call from multiple goroutines.
func (c *Conn) WriteMessage(header, message map[string]any) error {
	head, err := Marshal(header)
	if err != nil {
		return err
	}
	if len(head) > 0xFF {
		return errors.New("hds: message header too big")
	}

	body, err := Marshal(message)
	if err != nil {
		return err
	}

	if 1+len(head)+len(body) > maxPayloadSize {
		return errors.New("hds: message too big")
	}

	b := make([]byte, 0, 1+len(head)+len(body))
	b = append(b, byte(len(head)))
	b = append(b, head...)
	b = append(b, body...)

	c.wmu.Lock()
	_, err = c.Write(b)
	c.wmu.Unlock()
	return err
}

// WriteEvent writes an event message.
func (c *Conn) WriteEvent(protocol, topic string, message map[string]any) error {
	if message == nil {
		message = map[string]any{}
	}
	return c.WriteMessage(map[string]any{
		"protocol": protocol,
		"event":    topic,
	}, message)
}

// WriteResponse writes a response to a request with the given id.
func (c *Conn) WriteResponse(protocol, topic string, id, status int64, message map[string]any) error {
	if message == nil {
		message = map[string]any{}
	}
	return c.WriteMessage(map[string]any{
		"protocol": protocol,
		"response": topic,
		"id":       id,
		"status":   status,
	}, message)
}
