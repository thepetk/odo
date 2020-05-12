// Code generated by protoc-gen-go. DO NOT EDIT.
// source: google/ads/googleads/v0/enums/bidding_source.proto

package enums // import "google.golang.org/genproto/googleapis/ads/googleads/v0/enums"

import proto "github.com/golang/protobuf/proto"
import fmt "fmt"
import math "math"

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion2 // please upgrade the proto package

// Enum describing possible bidding sources.
type BiddingSourceEnum_BiddingSource int32

const (
	// Not specified.
	BiddingSourceEnum_UNSPECIFIED BiddingSourceEnum_BiddingSource = 0
	// Used for return value only. Represents value unknown in this version.
	BiddingSourceEnum_UNKNOWN BiddingSourceEnum_BiddingSource = 1
	// Bidding entity is defined on the ad group.
	BiddingSourceEnum_ADGROUP BiddingSourceEnum_BiddingSource = 2
	// Bidding entity is defined on the ad group criterion.
	BiddingSourceEnum_CRITERION BiddingSourceEnum_BiddingSource = 3
	// Effective bidding entity is inherited from campaign bidding strategy.
	BiddingSourceEnum_CAMPAIGN_BIDDING_STRATEGY BiddingSourceEnum_BiddingSource = 5
)

var BiddingSourceEnum_BiddingSource_name = map[int32]string{
	0: "UNSPECIFIED",
	1: "UNKNOWN",
	2: "ADGROUP",
	3: "CRITERION",
	5: "CAMPAIGN_BIDDING_STRATEGY",
}
var BiddingSourceEnum_BiddingSource_value = map[string]int32{
	"UNSPECIFIED":               0,
	"UNKNOWN":                   1,
	"ADGROUP":                   2,
	"CRITERION":                 3,
	"CAMPAIGN_BIDDING_STRATEGY": 5,
}

func (x BiddingSourceEnum_BiddingSource) String() string {
	return proto.EnumName(BiddingSourceEnum_BiddingSource_name, int32(x))
}
func (BiddingSourceEnum_BiddingSource) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_bidding_source_b3968060a90d9b4f, []int{0, 0}
}

// Container for enum describing possible bidding sources.
type BiddingSourceEnum struct {
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *BiddingSourceEnum) Reset()         { *m = BiddingSourceEnum{} }
func (m *BiddingSourceEnum) String() string { return proto.CompactTextString(m) }
func (*BiddingSourceEnum) ProtoMessage()    {}
func (*BiddingSourceEnum) Descriptor() ([]byte, []int) {
	return fileDescriptor_bidding_source_b3968060a90d9b4f, []int{0}
}
func (m *BiddingSourceEnum) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_BiddingSourceEnum.Unmarshal(m, b)
}
func (m *BiddingSourceEnum) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_BiddingSourceEnum.Marshal(b, m, deterministic)
}
func (dst *BiddingSourceEnum) XXX_Merge(src proto.Message) {
	xxx_messageInfo_BiddingSourceEnum.Merge(dst, src)
}
func (m *BiddingSourceEnum) XXX_Size() int {
	return xxx_messageInfo_BiddingSourceEnum.Size(m)
}
func (m *BiddingSourceEnum) XXX_DiscardUnknown() {
	xxx_messageInfo_BiddingSourceEnum.DiscardUnknown(m)
}

var xxx_messageInfo_BiddingSourceEnum proto.InternalMessageInfo

func init() {
	proto.RegisterType((*BiddingSourceEnum)(nil), "google.ads.googleads.v0.enums.BiddingSourceEnum")
	proto.RegisterEnum("google.ads.googleads.v0.enums.BiddingSourceEnum_BiddingSource", BiddingSourceEnum_BiddingSource_name, BiddingSourceEnum_BiddingSource_value)
}

func init() {
	proto.RegisterFile("google/ads/googleads/v0/enums/bidding_source.proto", fileDescriptor_bidding_source_b3968060a90d9b4f)
}

var fileDescriptor_bidding_source_b3968060a90d9b4f = []byte{
	// 313 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x7c, 0x50, 0x4d, 0x4e, 0xf3, 0x30,
	0x14, 0xfc, 0x9a, 0xea, 0x03, 0xe1, 0xaa, 0x22, 0x78, 0xc7, 0xa2, 0x8b, 0xf6, 0x00, 0x4e, 0x04,
	0x3b, 0xb3, 0x72, 0x9a, 0x60, 0x59, 0x08, 0x37, 0x4a, 0x7f, 0x10, 0x28, 0x52, 0xd5, 0xd6, 0x91,
	0x89, 0xd4, 0xc4, 0x55, 0x4c, 0xba, 0xe3, 0x32, 0x2c, 0x39, 0x0a, 0x17, 0x41, 0xe2, 0x14, 0x28,
	0x36, 0x8d, 0xd4, 0x05, 0x6c, 0xac, 0xf1, 0x9b, 0x99, 0xa7, 0x79, 0x03, 0xae, 0xa4, 0x52, 0x72,
	0x9b, 0x79, 0x2b, 0xa1, 0x3d, 0x0b, 0x1b, 0xb4, 0xf7, 0xbd, 0xac, 0xac, 0x0b, 0xed, 0xad, 0x73,
	0x21, 0xf2, 0x52, 0x2e, 0xb5, 0xaa, 0xab, 0x4d, 0x86, 0x76, 0x95, 0x7a, 0x51, 0x70, 0x60, 0x85,
	0x68, 0x25, 0x34, 0x6a, 0x3d, 0x68, 0xef, 0x23, 0xe3, 0x19, 0xbd, 0x82, 0x8b, 0xc0, 0xda, 0xa6,
	0xc6, 0x15, 0x95, 0x75, 0x31, 0x7a, 0x06, 0xfd, 0xa3, 0x21, 0x3c, 0x07, 0xbd, 0x39, 0x9f, 0xc6,
	0xd1, 0x98, 0xdd, 0xb2, 0x28, 0x74, 0xff, 0xc1, 0x1e, 0x38, 0x9d, 0xf3, 0x3b, 0x3e, 0x79, 0xe0,
	0x6e, 0xa7, 0xf9, 0x90, 0x90, 0x26, 0x93, 0x79, 0xec, 0x3a, 0xb0, 0x0f, 0xce, 0xc6, 0x09, 0x9b,
	0x45, 0x09, 0x9b, 0x70, 0xb7, 0x0b, 0x07, 0xe0, 0x72, 0x4c, 0xee, 0x63, 0xc2, 0x28, 0x5f, 0x06,
	0x2c, 0x0c, 0x19, 0xa7, 0xcb, 0xe9, 0x2c, 0x21, 0xb3, 0x88, 0x3e, 0xba, 0xff, 0x83, 0xcf, 0x0e,
	0x18, 0x6e, 0x54, 0x81, 0xfe, 0x0c, 0x19, 0xc0, 0xa3, 0x34, 0x71, 0x73, 0x57, 0xdc, 0x79, 0x0a,
	0x7e, 0x4c, 0x52, 0x6d, 0x57, 0xa5, 0x44, 0xaa, 0x92, 0x9e, 0xcc, 0x4a, 0x73, 0xf5, 0xa1, 0x9d,
	0x5d, 0xae, 0x7f, 0x29, 0xeb, 0xc6, 0xbc, 0x6f, 0x4e, 0x97, 0x12, 0xf2, 0xee, 0x0c, 0xa8, 0x5d,
	0x45, 0x84, 0x46, 0x16, 0x36, 0x68, 0xe1, 0xa3, 0xa6, 0x0e, 0xfd, 0x71, 0xe0, 0x53, 0x22, 0x74,
	0xda, 0xf2, 0xe9, 0xc2, 0x4f, 0x0d, 0xff, 0xe5, 0x0c, 0xed, 0x10, 0x63, 0x22, 0x34, 0xc6, 0xad,
	0x02, 0xe3, 0x85, 0x8f, 0xb1, 0xd1, 0xac, 0x4f, 0x4c, 0xb0, 0xeb, 0xef, 0x00, 0x00, 0x00, 0xff,
	0xff, 0x17, 0x3d, 0xe8, 0xc8, 0xc4, 0x01, 0x00, 0x00,
}