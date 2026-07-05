package homekit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/AlexxIT/go2rtc/pkg/hap"
	"github.com/AlexxIT/go2rtc/pkg/hap/tlv8"
)

type HandlerFunc func(net.Conn) error

type Server interface {
	ServerPair
	ServerAccessory
}

type ServerPair interface {
	GetPair(id string) []byte
	AddPair(id string, public []byte, permissions byte)
	DelPair(id string)
}

type ServerAccessory interface {
	GetAccessories(conn net.Conn) []*hap.Accessory
	GetCharacteristic(conn net.Conn, aid uint8, iid uint64) any
	SetCharacteristic(conn net.Conn, aid uint8, iid uint64, value any)
	GetImage(conn net.Conn, width, height int) []byte
}

// ServerEvents - optional interface for servers with support for
// event notifications ("ev" subscriptions).
type ServerEvents interface {
	SetEventCharacteristic(conn net.Conn, aid uint8, iid uint64, enable bool)
}

// ServerWriteResponse - optional interface for servers with support for
// write-response characteristics (ex. SetupDataStreamTransport).
// Should return the new characteristic value for the response.
type ServerWriteResponse interface {
	SetCharacteristicWR(conn net.Conn, aid uint8, iid uint64, value any) any
}

func ServerHandler(server Server) HandlerFunc {
	return handleRequest(func(conn net.Conn, req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case hap.PathPairings:
			return handlePairings(req, server)

		case hap.PathAccessories:
			body := hap.JSONAccessories{Value: server.GetAccessories(conn)}
			return makeResponse(hap.MimeJSON, body)

		case hap.PathCharacteristics:
			switch req.Method {
			case "GET":
				var v hap.JSONCharacters

				id := req.URL.Query().Get("id")
				for _, id = range strings.Split(id, ",") {
					s1, s2, _ := strings.Cut(id, ".")
					aid, _ := strconv.Atoi(s1)
					iid, _ := strconv.ParseUint(s2, 10, 64)
					val := server.GetCharacteristic(conn, uint8(aid), iid)

					v.Value = append(v.Value, hap.JSONCharacter{AID: uint8(aid), IID: iid, Value: val})
				}

				return makeResponse(hap.MimeJSON, v)

			case "PUT":
				var v struct {
					Value []struct {
						AID   uint8  `json:"aid"`
						IID   uint64 `json:"iid"`
						Value any    `json:"value"`
						Event *bool  `json:"ev"`
						WR    bool   `json:"r"`
					} `json:"characteristics"`
				}
				if err := json.NewDecoder(req.Body).Decode(&v); err != nil {
					return nil, err
				}

				var writeResponse bool

				for _, char := range v.Value {
					if char.Event != nil {
						if events, ok := server.(ServerEvents); ok {
							events.SetEventCharacteristic(conn, char.AID, char.IID, *char.Event)
						}
					}
					if char.WR {
						writeResponse = true
					}
				}

				if !writeResponse {
					for _, char := range v.Value {
						if char.Value != nil {
							server.SetCharacteristic(conn, char.AID, char.IID, char.Value)
						}
					}

					res := &http.Response{
						StatusCode: http.StatusNoContent,
						Proto:      "HTTP",
						ProtoMajor: 1,
						ProtoMinor: 1,
					}
					return res, nil
				}

				// write-response requires 207 Multi-Status with values
				var body hap.JSONCharacters
				for _, char := range v.Value {
					var value any
					if char.Value != nil {
						if wr, ok := server.(ServerWriteResponse); ok && char.WR {
							value = wr.SetCharacteristicWR(conn, char.AID, char.IID, char.Value)
						} else {
							server.SetCharacteristic(conn, char.AID, char.IID, char.Value)
						}
					}
					body.Value = append(body.Value, hap.JSONCharacter{
						AID: char.AID, IID: char.IID, Status: 0, Value: value,
					})
				}

				res, err := makeResponse(hap.MimeJSON, body)
				if err == nil {
					res.StatusCode = http.StatusMultiStatus
				}
				return res, err
			}

		case hap.PathResource:
			var v struct {
				Width  int    `json:"image-width"`
				Height int    `json:"image-height"`
				Type   string `json:"resource-type"`
			}
			if err := json.NewDecoder(req.Body).Decode(&v); err != nil {
				return nil, err
			}

			body := server.GetImage(conn, v.Width, v.Height)
			return makeResponse("image/jpeg", body)
		}

		return nil, errors.New("hap: unsupported path: " + req.RequestURI)
	})
}

func handleRequest(handle func(conn net.Conn, req *http.Request) (*http.Response, error)) HandlerFunc {
	return func(conn net.Conn) error {
		rd := bufio.NewReaderSize(conn, 16*1024)
		wr := bytes.NewBuffer(nil)
		for {
			req, err := http.ReadRequest(rd)
			//debug(req)
			if err != nil {
				return err
			}

			res, err := handle(conn, req)
			//debug(res)
			if err != nil {
				return err
			}

			// hap.Conn.Write holds the write mutex for the whole message,
			// event notifications go through the same lock, so buffering
			// the response into a single Write keeps HTTP messages and
			// events from interleaving on the encrypted session
			wr.Reset()
			if err = res.Write(wr); err != nil {
				return err
			}
			if _, err = conn.Write(wr.Bytes()); err != nil {
				return err
			}
		}
	}
}

func handlePairings(req *http.Request, srv ServerPair) (*http.Response, error) {
	cmd := struct {
		Method      byte   `tlv8:"0"`
		Identifier  string `tlv8:"1"`
		PublicKey   string `tlv8:"3"`
		State       byte   `tlv8:"6"`
		Permissions byte   `tlv8:"11"`
	}{}

	if err := tlv8.UnmarshalReader(req.Body, req.ContentLength, &cmd); err != nil {
		return nil, err
	}

	switch cmd.Method {
	case 3: // add
		srv.AddPair(cmd.Identifier, []byte(cmd.PublicKey), cmd.Permissions)
	case 4: // delete
		srv.DelPair(cmd.Identifier)
	}

	body := struct {
		State byte `tlv8:"6"`
	}{
		State: hap.StateM2,
	}

	return makeResponse(hap.MimeTLV8, body)
}

func makeResponse(mime string, v any) (*http.Response, error) {
	var body []byte
	var err error

	switch mime {
	case hap.MimeJSON:
		body, err = json.Marshal(v)
	case hap.MimeTLV8:
		body, err = tlv8.Marshal(v)
	case "image/jpeg":
		body = v.([]byte)
	}

	if err != nil {
		return nil, err
	}

	res := &http.Response{
		StatusCode: http.StatusOK,
		Proto:      "HTTP",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header: http.Header{
			"Content-Type":   []string{mime},
			"Content-Length": []string{strconv.Itoa(len(body))},
		},
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(bytes.NewReader(body)),
	}
	return res, nil
}
