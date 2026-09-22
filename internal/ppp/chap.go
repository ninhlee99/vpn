package ppp

import "fmt"

// CHAP packet codes, RFC 1994 §4.
const (
	CHAPCodeChallenge = 1
	CHAPCodeResponse  = 2
	CHAPCodeSuccess   = 3
	CHAPCodeFailure   = 4
)

// CHAPPacket is the common CHAP layout (RFC 1994 §4): Code, Identifier,
// Length, then a type-specific body. For Challenge/Response, the body is
// ValueSize(1) + Value + Name; for Success/Failure it's a Message string.
type CHAPPacket struct {
	Code       uint8
	Identifier uint8
	Value      []byte // Challenge or Response value
	Name       []byte // sender's name (Challenge/Response only)
	Message    []byte // Success/Failure only
}

func (p CHAPPacket) Marshal() []byte {
	var data []byte
	switch p.Code {
	case CHAPCodeChallenge, CHAPCodeResponse:
		data = make([]byte, 1+len(p.Value)+len(p.Name))
		data[0] = uint8(len(p.Value))
		copy(data[1:], p.Value)
		copy(data[1+len(p.Value):], p.Name)
	case CHAPCodeSuccess, CHAPCodeFailure:
		data = p.Message
	}
	return ControlPacket{Code: p.Code, Identifier: p.Identifier, Data: data}.Marshal()
}

func ParseCHAPPacket(b []byte) (CHAPPacket, error) {
	cp, err := ParseControlPacket(b)
	if err != nil {
		return CHAPPacket{}, err
	}
	p := CHAPPacket{Code: cp.Code, Identifier: cp.Identifier}
	switch cp.Code {
	case CHAPCodeChallenge, CHAPCodeResponse:
		if len(cp.Data) < 1 {
			return CHAPPacket{}, fmt.Errorf("CHAP Challenge/Response missing ValueSize")
		}
		valueSize := int(cp.Data[0])
		if 1+valueSize > len(cp.Data) {
			return CHAPPacket{}, fmt.Errorf("CHAP ValueSize %d exceeds packet", valueSize)
		}
		p.Value = cp.Data[1 : 1+valueSize]
		p.Name = cp.Data[1+valueSize:]
	case CHAPCodeSuccess, CHAPCodeFailure:
		p.Message = cp.Data
	}
	return p, nil
}
