package hds

// HomeKit Data Stream (HDS) binary serialization format.
//
// The format is a compact, tag-based encoding used inside HDS payloads
// (an OPACK-like format). This implementation is compatible with the
// reference implementation in HAP-NodeJS (DataStreamParser.ts) and with
// Apple HomeKit controllers.
//
// Go type mapping:
//
//	nil            <-> null
//	bool           <-> true/false
//	int64          <-> integer forms (small int, int8/16/32/64 LE)
//	float64        <-> float64 LE (float32 decoded into float64)
//	string         <-> utf8 forms
//	[]byte         <-> data forms
//	[]any          <-> array forms
//	map[string]any <-> dictionary forms
//
// The encoder never emits back-reference "compression" tags, which is always
// valid. The decoder fully supports them because Apple controllers use them.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	tagInvalid    = 0x00
	tagTrue       = 0x01
	tagFalse      = 0x02
	tagTerminator = 0x03
	tagNull       = 0x04
	tagUUID       = 0x05
	tagDate       = 0x06

	tagIntMinusOne = 0x07
	tagIntZero     = 0x08 // 0x08..0x2E - small integers 0..38
	tagIntMax      = 0x2E
	tagInt8        = 0x30
	tagInt16LE     = 0x31
	tagInt32LE     = 0x32
	tagInt64LE     = 0x33

	tagFloat32LE = 0x35
	tagFloat64LE = 0x36

	tagUTF8Start   = 0x40 // 0x40..0x60 - utf8 with length 0..32
	tagUTF8Stop    = 0x60
	tagUTF8Len8    = 0x61
	tagUTF8Len16LE = 0x62
	tagUTF8Len32LE = 0x63
	tagUTF8Len64LE = 0x64
	tagUTF8Null    = 0x6F

	tagDataStart   = 0x70 // 0x70..0x90 - data with length 0..32
	tagDataStop    = 0x90
	tagDataLen8    = 0x91
	tagDataLen16LE = 0x92
	tagDataLen32LE = 0x93
	tagDataLen64LE = 0x94
	tagDataTerm    = 0x9F

	tagComprStart = 0xA0 // 0xA0..0xCF - back-reference to previously decoded value
	tagComprStop  = 0xCF

	tagArrayStart = 0xD0 // 0xD0..0xDE - array with count 0..14
	tagArrayStop  = 0xDE
	tagArrayTerm  = 0xDF

	tagDictStart = 0xE0 // 0xE0..0xEE - dictionary with count 0..14
	tagDictStop  = 0xEE
	tagDictTerm  = 0xEF
)

// terminator is an internal decoder marker for tagTerminator.
type terminator struct{}

// Marshal encodes value to the HDS binary format.
func Marshal(v any) ([]byte, error) {
	return marshalTo(nil, v)
}

func marshalTo(b []byte, v any) ([]byte, error) {
	switch v := v.(type) {
	case nil:
		return append(b, tagNull), nil

	case bool:
		if v {
			return append(b, tagTrue), nil
		}
		return append(b, tagFalse), nil

	case int:
		return marshalInt(b, int64(v)), nil
	case int8:
		return marshalInt(b, int64(v)), nil
	case int16:
		return marshalInt(b, int64(v)), nil
	case int32:
		return marshalInt(b, int64(v)), nil
	case int64:
		return marshalInt(b, v), nil
	case uint:
		return marshalInt(b, int64(v)), nil
	case uint8:
		return marshalInt(b, int64(v)), nil
	case uint16:
		return marshalInt(b, int64(v)), nil
	case uint32:
		return marshalInt(b, int64(v)), nil
	case uint64:
		return marshalInt(b, int64(v)), nil

	case float32:
		b = append(b, tagFloat32LE)
		return binary.LittleEndian.AppendUint32(b, math.Float32bits(v)), nil
	case float64:
		b = append(b, tagFloat64LE)
		return binary.LittleEndian.AppendUint64(b, math.Float64bits(v)), nil

	case string:
		return marshalString(b, v), nil

	case []byte:
		return marshalData(b, v), nil

	case []any:
		var err error
		if n := len(v); n <= 12 {
			b = append(b, byte(tagArrayStart+n))
			for _, item := range v {
				if b, err = marshalTo(b, item); err != nil {
					return nil, err
				}
			}
		} else {
			b = append(b, tagArrayTerm)
			for _, item := range v {
				if b, err = marshalTo(b, item); err != nil {
					return nil, err
				}
			}
			b = append(b, tagTerminator)
		}
		return b, nil

	case map[string]any:
		var err error
		if n := len(v); n <= 14 {
			b = append(b, byte(tagDictStart+n))
			for key, item := range v {
				b = marshalString(b, key)
				if b, err = marshalTo(b, item); err != nil {
					return nil, err
				}
			}
		} else {
			b = append(b, tagDictTerm)
			for key, item := range v {
				b = marshalString(b, key)
				if b, err = marshalTo(b, item); err != nil {
					return nil, err
				}
			}
			b = append(b, tagTerminator)
		}
		return b, nil
	}

	return nil, fmt.Errorf("hds: can't marshal type %T", v)
}

func marshalInt(b []byte, v int64) []byte {
	switch {
	case v == -1:
		return append(b, tagIntMinusOne)
	case v >= 0 && v <= 38:
		return append(b, byte(tagIntZero+v))
	case v >= math.MinInt8 && v <= math.MaxInt8:
		return append(b, tagInt8, byte(v))
	case v >= math.MinInt16 && v <= math.MaxInt16:
		return binary.LittleEndian.AppendUint16(append(b, tagInt16LE), uint16(v))
	case v >= math.MinInt32 && v <= math.MaxInt32:
		return binary.LittleEndian.AppendUint32(append(b, tagInt32LE), uint32(v))
	default:
		return binary.LittleEndian.AppendUint64(append(b, tagInt64LE), uint64(v))
	}
}

func marshalString(b []byte, v string) []byte {
	switch n := len(v); {
	case n <= 32:
		b = append(b, byte(tagUTF8Start+n))
	case n <= math.MaxUint8:
		b = append(b, tagUTF8Len8, byte(n))
	case n <= math.MaxUint16:
		b = binary.LittleEndian.AppendUint16(append(b, tagUTF8Len16LE), uint16(n))
	default:
		b = binary.LittleEndian.AppendUint32(append(b, tagUTF8Len32LE), uint32(n))
	}
	return append(b, v...)
}

func marshalData(b, v []byte) []byte {
	switch n := len(v); {
	case n <= 32:
		b = append(b, byte(tagDataStart+n))
	case n <= math.MaxUint8:
		b = append(b, tagDataLen8, byte(n))
	case n <= math.MaxUint16:
		b = binary.LittleEndian.AppendUint16(append(b, tagDataLen16LE), uint16(n))
	default:
		b = binary.LittleEndian.AppendUint32(append(b, tagDataLen32LE), uint32(n))
	}
	return append(b, v...)
}

// Unmarshal decodes a single value from the HDS binary format.
func Unmarshal(b []byte) (any, error) {
	d := decoder{b: b}
	v, err := d.decode()
	if err != nil {
		return nil, err
	}
	if _, ok := v.(terminator); ok {
		return nil, errors.New("hds: unexpected terminator")
	}
	return v, nil
}

type decoder struct {
	b []byte
	i int
	// tracked values for back-reference "compression" tags,
	// in the exact same order as the reference implementation
	tracked []any
}

func (d *decoder) track(v any) any {
	d.tracked = append(d.tracked, v)
	return v
}

func (d *decoder) read(n int) ([]byte, error) {
	if d.i+n > len(d.b) {
		return nil, errors.New("hds: unexpected end of data")
	}
	v := d.b[d.i : d.i+n]
	d.i += n
	return v, nil
}

func (d *decoder) readLen(size int) (int, error) {
	v, err := d.read(size)
	if err != nil {
		return 0, err
	}
	var n uint64
	for i := size - 1; i >= 0; i-- {
		n = n<<8 | uint64(v[i]) // little-endian
	}
	if n > math.MaxInt32 {
		return 0, errors.New("hds: length too big")
	}
	return int(n), nil
}

func (d *decoder) decode() (any, error) {
	tag, err := d.read(1)
	if err != nil {
		return nil, err
	}

	t := tag[0]
	switch {
	case t == tagInvalid:
		return nil, errors.New("hds: invalid zero tag")
	case t == tagTrue:
		return d.track(true), nil
	case t == tagFalse:
		return d.track(false), nil
	case t == tagTerminator:
		return terminator{}, nil
	case t == tagNull:
		return nil, nil
	case t == tagUUID:
		v, err := d.read(16)
		if err != nil {
			return nil, err
		}
		return d.track(fmt.Sprintf(
			"%x-%x-%x-%x-%x", v[0:4], v[4:6], v[6:8], v[8:10], v[10:16],
		)), nil
	case t == tagDate:
		v, err := d.read(8)
		if err != nil {
			return nil, err
		}
		return d.track(math.Float64frombits(binary.LittleEndian.Uint64(v))), nil

	case t == tagIntMinusOne:
		return d.track(int64(-1)), nil
	case t >= tagIntZero && t <= tagIntMax:
		return d.track(int64(t - tagIntZero)), nil
	case t == tagInt8:
		v, err := d.read(1)
		if err != nil {
			return nil, err
		}
		return d.track(int64(int8(v[0]))), nil
	case t == tagInt16LE:
		v, err := d.read(2)
		if err != nil {
			return nil, err
		}
		return d.track(int64(int16(binary.LittleEndian.Uint16(v)))), nil
	case t == tagInt32LE:
		v, err := d.read(4)
		if err != nil {
			return nil, err
		}
		return d.track(int64(int32(binary.LittleEndian.Uint32(v)))), nil
	case t == tagInt64LE:
		v, err := d.read(8)
		if err != nil {
			return nil, err
		}
		return d.track(int64(binary.LittleEndian.Uint64(v))), nil

	case t == tagFloat32LE:
		v, err := d.read(4)
		if err != nil {
			return nil, err
		}
		return d.track(float64(math.Float32frombits(binary.LittleEndian.Uint32(v)))), nil
	case t == tagFloat64LE:
		v, err := d.read(8)
		if err != nil {
			return nil, err
		}
		return d.track(math.Float64frombits(binary.LittleEndian.Uint64(v))), nil

	case t >= tagUTF8Start && t <= tagUTF8Stop:
		return d.decodeString(int(t - tagUTF8Start))
	case t == tagUTF8Len8:
		n, err := d.readLen(1)
		if err != nil {
			return nil, err
		}
		return d.decodeString(n)
	case t == tagUTF8Len16LE:
		n, err := d.readLen(2)
		if err != nil {
			return nil, err
		}
		return d.decodeString(n)
	case t == tagUTF8Len32LE:
		n, err := d.readLen(4)
		if err != nil {
			return nil, err
		}
		return d.decodeString(n)
	case t == tagUTF8Len64LE:
		n, err := d.readLen(8)
		if err != nil {
			return nil, err
		}
		return d.decodeString(n)
	case t == tagUTF8Null:
		for n := d.i; n < len(d.b); n++ {
			if d.b[n] == 0 {
				v := string(d.b[d.i:n])
				d.i = n + 1
				return d.track(v), nil
			}
		}
		return nil, errors.New("hds: missing null terminator")

	case t >= tagDataStart && t <= tagDataStop:
		return d.decodeData(int(t - tagDataStart))
	case t == tagDataLen8:
		n, err := d.readLen(1)
		if err != nil {
			return nil, err
		}
		return d.decodeData(n)
	case t == tagDataLen16LE:
		n, err := d.readLen(2)
		if err != nil {
			return nil, err
		}
		return d.decodeData(n)
	case t == tagDataLen32LE:
		n, err := d.readLen(4)
		if err != nil {
			return nil, err
		}
		return d.decodeData(n)
	case t == tagDataLen64LE:
		n, err := d.readLen(8)
		if err != nil {
			return nil, err
		}
		return d.decodeData(n)
	case t == tagDataTerm:
		for n := d.i; n < len(d.b); n++ {
			if d.b[n] == tagTerminator {
				v := append([]byte(nil), d.b[d.i:n]...)
				d.i = n + 1
				return d.track(v), nil
			}
		}
		return nil, errors.New("hds: missing data terminator")

	case t >= tagComprStart && t <= tagComprStop:
		i := int(t - tagComprStart)
		if i >= len(d.tracked) {
			return nil, errors.New("hds: back-reference out of range")
		}
		return d.tracked[i], nil

	case t >= tagArrayStart && t <= tagArrayStop:
		n := int(t - tagArrayStart)
		items := make([]any, 0, n)
		for i := 0; i < n; i++ {
			item, err := d.decode()
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	case t == tagArrayTerm:
		var items []any
		for {
			item, err := d.decode()
			if err != nil {
				return nil, err
			}
			if _, ok := item.(terminator); ok {
				return items, nil
			}
			items = append(items, item)
		}

	case t >= tagDictStart && t <= tagDictStop:
		n := int(t - tagDictStart)
		dict := make(map[string]any, n)
		for i := 0; i < n; i++ {
			if err := d.decodeEntry(dict); err != nil {
				return nil, err
			}
		}
		return dict, nil
	case t == tagDictTerm:
		dict := map[string]any{}
		for {
			key, err := d.decode()
			if err != nil {
				return nil, err
			}
			if _, ok := key.(terminator); ok {
				return dict, nil
			}
			s, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("hds: dict key should be string: %v", key)
			}
			value, err := d.decode()
			if err != nil {
				return nil, err
			}
			dict[s] = value
		}
	}

	return nil, fmt.Errorf("hds: unknown tag 0x%02X", t)
}

func (d *decoder) decodeString(n int) (any, error) {
	v, err := d.read(n)
	if err != nil {
		return nil, err
	}
	return d.track(string(v)), nil
}

func (d *decoder) decodeData(n int) (any, error) {
	v, err := d.read(n)
	if err != nil {
		return nil, err
	}
	return d.track(append([]byte(nil), v...)), nil
}

func (d *decoder) decodeEntry(dict map[string]any) error {
	key, err := d.decode()
	if err != nil {
		return err
	}
	s, ok := key.(string)
	if !ok {
		return fmt.Errorf("hds: dict key should be string: %v", key)
	}
	value, err := d.decode()
	if err != nil {
		return err
	}
	dict[s] = value
	return nil
}
