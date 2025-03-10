// Code generated by protoc-gen-gogo. DO NOT EDIT.
// source: pkg/api/testspec.proto

package api

import (
	fmt "fmt"
	io "io"
	math "math"
	math_bits "math/bits"
	reflect "reflect"
	strings "strings"
	time "time"

	_ "github.com/gogo/protobuf/gogoproto"
	proto "github.com/gogo/protobuf/proto"
	_ "github.com/gogo/protobuf/types"
	github_com_gogo_protobuf_types "github.com/gogo/protobuf/types"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf
var _ = time.Kitchen

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.GoGoProtoPackageIsVersion3 // please upgrade the proto package

// If the jobs in this spec. should be cancelled.
type TestSpec_Cancel int32

const (
	TestSpec_NO     TestSpec_Cancel = 0
	TestSpec_BY_ID  TestSpec_Cancel = 1
	TestSpec_BY_SET TestSpec_Cancel = 2
	TestSpec_BY_IDS TestSpec_Cancel = 3
)

var TestSpec_Cancel_name = map[int32]string{
	0: "NO",
	1: "BY_ID",
	2: "BY_SET",
	3: "BY_IDS",
}

var TestSpec_Cancel_value = map[string]int32{
	"NO":     0,
	"BY_ID":  1,
	"BY_SET": 2,
	"BY_IDS": 3,
}

func (x TestSpec_Cancel) String() string {
	return proto.EnumName(TestSpec_Cancel_name, int32(x))
}

func (TestSpec_Cancel) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_38d601305b414287, []int{0, 0}
}

// Defines a test case for the Armada test suite.
// Defined as a proto message to enable unmarshalling oneof fields.
type TestSpec struct {
	// Jobs to submit.
	// The n jobs herein are copied BatchSize times to produce n*BatchSize jobs.
	// A batch of n*BatchSize such jobs are submitted in each API call.
	// NumBatches such batches are submitted in total.
	Jobs []*JobSubmitRequestItem `protobuf:"bytes,1,rep,name=jobs,proto3" json:"jobs,omitempty"`
	// Events expected in response to submitting each job.
	ExpectedEvents []*EventMessage `protobuf:"bytes,2,rep,name=expected_events,json=expectedEvents,proto3" json:"expectedEvents,omitempty"`
	// Queue to submit jobs to.
	Queue string `protobuf:"bytes,3,opt,name=queue,proto3" json:"queue,omitempty"`
	// Job set to submit jobs to.
	JobSetId string `protobuf:"bytes,4,opt,name=job_set_id,json=jobSetId,proto3" json:"jobSetId,omitempty"`
	// Number of batches of jobs to submit.
	// If 0, will submit forever.
	NumBatches uint32 `protobuf:"varint,5,opt,name=num_batches,json=numBatches,proto3" json:"numBatches,omitempty"`
	// Number of copies of the provided jobs to submit per batch.
	BatchSize uint32 `protobuf:"varint,6,opt,name=batch_size,json=batchSize,proto3" json:"batchSize,omitempty"`
	// Time between batches.
	// If 0, jobs are submitted as quickly as possible.
	Interval time.Duration `protobuf:"bytes,7,opt,name=interval,proto3,stdduration" json:"interval"`
	// Number of seconds to wait for jobs to finish.
	Timeout time.Duration   `protobuf:"bytes,8,opt,name=timeout,proto3,stdduration" json:"timeout"`
	Cancel  TestSpec_Cancel `protobuf:"varint,9,opt,name=cancel,proto3,enum=api.TestSpec_Cancel" json:"cancel,omitempty"`
	// Test name. Defaults to the filename if not provided.
	Name string `protobuf:"bytes,10,opt,name=name,proto3" json:"name,omitempty"`
	// Randomize clientId if not provided
	RandomClientId bool `protobuf:"varint,11,opt,name=random_client_id,json=randomClientId,proto3" json:"randomClientId,omitempty"`
	// Toggle should testsuite scrape Armada Job (pod) logs
	GetLogs bool `protobuf:"varint,12,opt,name=get_logs,json=getLogs,proto3" json:"getLogs,omitempty"`
}

func (m *TestSpec) Reset()      { *m = TestSpec{} }
func (*TestSpec) ProtoMessage() {}
func (*TestSpec) Descriptor() ([]byte, []int) {
	return fileDescriptor_38d601305b414287, []int{0}
}
func (m *TestSpec) XXX_Unmarshal(b []byte) error {
	return m.Unmarshal(b)
}
func (m *TestSpec) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	if deterministic {
		return xxx_messageInfo_TestSpec.Marshal(b, m, deterministic)
	} else {
		b = b[:cap(b)]
		n, err := m.MarshalToSizedBuffer(b)
		if err != nil {
			return nil, err
		}
		return b[:n], nil
	}
}
func (m *TestSpec) XXX_Merge(src proto.Message) {
	xxx_messageInfo_TestSpec.Merge(m, src)
}
func (m *TestSpec) XXX_Size() int {
	return m.Size()
}
func (m *TestSpec) XXX_DiscardUnknown() {
	xxx_messageInfo_TestSpec.DiscardUnknown(m)
}

var xxx_messageInfo_TestSpec proto.InternalMessageInfo

func (m *TestSpec) GetJobs() []*JobSubmitRequestItem {
	if m != nil {
		return m.Jobs
	}
	return nil
}

func (m *TestSpec) GetExpectedEvents() []*EventMessage {
	if m != nil {
		return m.ExpectedEvents
	}
	return nil
}

func (m *TestSpec) GetQueue() string {
	if m != nil {
		return m.Queue
	}
	return ""
}

func (m *TestSpec) GetJobSetId() string {
	if m != nil {
		return m.JobSetId
	}
	return ""
}

func (m *TestSpec) GetNumBatches() uint32 {
	if m != nil {
		return m.NumBatches
	}
	return 0
}

func (m *TestSpec) GetBatchSize() uint32 {
	if m != nil {
		return m.BatchSize
	}
	return 0
}

func (m *TestSpec) GetInterval() time.Duration {
	if m != nil {
		return m.Interval
	}
	return 0
}

func (m *TestSpec) GetTimeout() time.Duration {
	if m != nil {
		return m.Timeout
	}
	return 0
}

func (m *TestSpec) GetCancel() TestSpec_Cancel {
	if m != nil {
		return m.Cancel
	}
	return TestSpec_NO
}

func (m *TestSpec) GetName() string {
	if m != nil {
		return m.Name
	}
	return ""
}

func (m *TestSpec) GetRandomClientId() bool {
	if m != nil {
		return m.RandomClientId
	}
	return false
}

func (m *TestSpec) GetGetLogs() bool {
	if m != nil {
		return m.GetLogs
	}
	return false
}

func init() {
	proto.RegisterEnum("api.TestSpec_Cancel", TestSpec_Cancel_name, TestSpec_Cancel_value)
	proto.RegisterType((*TestSpec)(nil), "api.TestSpec")
}

func init() { proto.RegisterFile("pkg/api/testspec.proto", fileDescriptor_38d601305b414287) }

var fileDescriptor_38d601305b414287 = []byte{
	// 528 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x8c, 0x90, 0xcd, 0x6e, 0xd3, 0x4e,
	0x14, 0xc5, 0x3d, 0xf9, 0x70, 0x9c, 0xc9, 0xff, 0x1f, 0xc2, 0x10, 0x21, 0xb7, 0x02, 0xc7, 0xea,
	0xca, 0x0b, 0xea, 0x48, 0xed, 0x02, 0x09, 0x09, 0xa1, 0xa6, 0xe9, 0xc2, 0x88, 0x2f, 0xd9, 0xdd,
	0x74, 0x65, 0x8d, 0xed, 0x8b, 0x3b, 0x21, 0xf6, 0xb8, 0x9e, 0x71, 0x85, 0xba, 0xe2, 0x11, 0x58,
	0xf2, 0x02, 0x6c, 0x78, 0x92, 0x2e, 0xbb, 0xec, 0x8a, 0x8f, 0xe4, 0x45, 0x50, 0xc7, 0x36, 0x6c,
	0xd9, 0xdd, 0xfb, 0x3b, 0xe7, 0xcc, 0xd5, 0x1c, 0xfc, 0xb0, 0xf8, 0x90, 0xce, 0x69, 0xc1, 0xe6,
	0x12, 0x84, 0x14, 0x05, 0xc4, 0x6e, 0x51, 0x72, 0xc9, 0x49, 0x97, 0x16, 0x6c, 0xd7, 0x4a, 0x39,
	0x4f, 0xd7, 0x30, 0x57, 0x28, 0xaa, 0xde, 0xcf, 0x93, 0xaa, 0xa4, 0x92, 0xf1, 0xbc, 0x36, 0xed,
	0xee, 0xa7, 0x4c, 0x9e, 0x57, 0x91, 0x1b, 0xf3, 0x6c, 0x9e, 0xf2, 0x94, 0xff, 0x35, 0xde, 0x6d,
	0x6a, 0x51, 0x53, 0x63, 0x9f, 0xb6, 0xb7, 0x44, 0x15, 0x65, 0x4c, 0x36, 0xf4, 0x41, 0x4b, 0xe1,
	0x12, 0xf2, 0x06, 0xee, 0x7d, 0xed, 0x61, 0xe3, 0x14, 0x84, 0x0c, 0x0a, 0x88, 0xc9, 0x3e, 0xee,
	0xad, 0x78, 0x24, 0x4c, 0x64, 0x77, 0x9d, 0xd1, 0xc1, 0x8e, 0x4b, 0x0b, 0xe6, 0xbe, 0xe4, 0x51,
	0xa0, 0x5e, 0xf1, 0xe1, 0xa2, 0x02, 0x21, 0x3d, 0x09, 0x99, 0xaf, 0x6c, 0xe4, 0x19, 0xbe, 0x07,
	0x1f, 0x0b, 0x88, 0x25, 0x24, 0xa1, 0x7a, 0x53, 0x98, 0x1d, 0x95, 0xbc, 0xaf, 0x92, 0x27, 0x77,
	0xe8, 0x35, 0x08, 0x41, 0x53, 0xf0, 0xc7, 0xad, 0x53, 0x51, 0x41, 0xa6, 0xb8, 0x7f, 0x51, 0x41,
	0x05, 0x66, 0xd7, 0x46, 0xce, 0xd0, 0xaf, 0x17, 0xf2, 0x08, 0xe3, 0x15, 0x8f, 0x42, 0x01, 0x32,
	0x64, 0x89, 0xd9, 0x53, 0x92, 0xb1, 0xe2, 0x51, 0x00, 0xd2, 0x4b, 0xc8, 0x0c, 0x8f, 0xf2, 0x2a,
	0x0b, 0x23, 0x2a, 0xe3, 0x73, 0x10, 0x66, 0xdf, 0x46, 0xce, 0xff, 0x3e, 0xce, 0xab, 0x6c, 0x51,
	0x13, 0xf2, 0x18, 0x63, 0x25, 0x86, 0x82, 0x5d, 0x81, 0xa9, 0x2b, 0x7d, 0xa8, 0x48, 0xc0, 0xae,
	0x80, 0xbc, 0xc0, 0x06, 0xcb, 0x25, 0x94, 0x97, 0x74, 0x6d, 0x0e, 0x6c, 0xa4, 0xbe, 0x58, 0x17,
	0xef, 0xb6, 0x7d, 0xba, 0xcb, 0xa6, 0xf8, 0x85, 0x71, 0xfd, 0x7d, 0xa6, 0x7d, 0xf9, 0x31, 0x43,
	0xfe, 0x9f, 0x10, 0x79, 0x8e, 0x07, 0x92, 0x65, 0xc0, 0x2b, 0x69, 0x1a, 0xff, 0x9e, 0x6f, 0x33,
	0xe4, 0x09, 0xd6, 0x63, 0x9a, 0xc7, 0xb0, 0x36, 0x87, 0x36, 0x72, 0xc6, 0x07, 0x53, 0x55, 0x53,
	0xdb, 0xbe, 0x7b, 0xac, 0x34, 0xbf, 0xf1, 0x10, 0x82, 0x7b, 0x39, 0xcd, 0xc0, 0xc4, 0xaa, 0x05,
	0x35, 0x13, 0x07, 0x4f, 0x4a, 0x9a, 0x27, 0x3c, 0x0b, 0xe3, 0x35, 0x83, 0x5c, 0xb5, 0x34, 0xb2,
	0x91, 0x63, 0xf8, 0xe3, 0x9a, 0x1f, 0x2b, 0xec, 0x25, 0x64, 0x07, 0x1b, 0x29, 0xc8, 0x70, 0xcd,
	0x53, 0x61, 0xfe, 0xa7, 0x1c, 0x83, 0x14, 0xe4, 0x2b, 0x9e, 0x8a, 0xbd, 0x43, 0xac, 0xd7, 0xa7,
	0x88, 0x8e, 0x3b, 0x6f, 0xde, 0x4e, 0x34, 0x32, 0xc4, 0xfd, 0xc5, 0x59, 0xe8, 0x2d, 0x27, 0x88,
	0x60, 0xac, 0x2f, 0xce, 0xc2, 0xe0, 0xe4, 0x74, 0xd2, 0x69, 0x66, 0x6f, 0x19, 0x4c, 0xba, 0x8b,
	0xa7, 0xb7, 0xbf, 0x2c, 0xed, 0xd3, 0xc6, 0x42, 0xd7, 0x1b, 0x0b, 0xdd, 0x6c, 0x2c, 0xf4, 0x73,
	0x63, 0xa1, 0xcf, 0x5b, 0x4b, 0xbb, 0xd9, 0x5a, 0xda, 0xed, 0xd6, 0xd2, 0xbe, 0x75, 0xa6, 0x47,
	0x65, 0x46, 0x13, 0xfa, 0xae, 0xe4, 0x2b, 0x88, 0xa5, 0xeb, 0x71, 0xf7, 0xa8, 0x60, 0x91, 0xae,
	0xaa, 0x39, 0xfc, 0x1d, 0x00, 0x00, 0xff, 0xff, 0xe2, 0x9f, 0x47, 0x9e, 0x00, 0x03, 0x00, 0x00,
}

func (m *TestSpec) Marshal() (dAtA []byte, err error) {
	size := m.Size()
	dAtA = make([]byte, size)
	n, err := m.MarshalToSizedBuffer(dAtA[:size])
	if err != nil {
		return nil, err
	}
	return dAtA[:n], nil
}

func (m *TestSpec) MarshalTo(dAtA []byte) (int, error) {
	size := m.Size()
	return m.MarshalToSizedBuffer(dAtA[:size])
}

func (m *TestSpec) MarshalToSizedBuffer(dAtA []byte) (int, error) {
	i := len(dAtA)
	_ = i
	var l int
	_ = l
	if m.GetLogs {
		i--
		if m.GetLogs {
			dAtA[i] = 1
		} else {
			dAtA[i] = 0
		}
		i--
		dAtA[i] = 0x60
	}
	if m.RandomClientId {
		i--
		if m.RandomClientId {
			dAtA[i] = 1
		} else {
			dAtA[i] = 0
		}
		i--
		dAtA[i] = 0x58
	}
	if len(m.Name) > 0 {
		i -= len(m.Name)
		copy(dAtA[i:], m.Name)
		i = encodeVarintTestspec(dAtA, i, uint64(len(m.Name)))
		i--
		dAtA[i] = 0x52
	}
	if m.Cancel != 0 {
		i = encodeVarintTestspec(dAtA, i, uint64(m.Cancel))
		i--
		dAtA[i] = 0x48
	}
	n1, err1 := github_com_gogo_protobuf_types.StdDurationMarshalTo(m.Timeout, dAtA[i-github_com_gogo_protobuf_types.SizeOfStdDuration(m.Timeout):])
	if err1 != nil {
		return 0, err1
	}
	i -= n1
	i = encodeVarintTestspec(dAtA, i, uint64(n1))
	i--
	dAtA[i] = 0x42
	n2, err2 := github_com_gogo_protobuf_types.StdDurationMarshalTo(m.Interval, dAtA[i-github_com_gogo_protobuf_types.SizeOfStdDuration(m.Interval):])
	if err2 != nil {
		return 0, err2
	}
	i -= n2
	i = encodeVarintTestspec(dAtA, i, uint64(n2))
	i--
	dAtA[i] = 0x3a
	if m.BatchSize != 0 {
		i = encodeVarintTestspec(dAtA, i, uint64(m.BatchSize))
		i--
		dAtA[i] = 0x30
	}
	if m.NumBatches != 0 {
		i = encodeVarintTestspec(dAtA, i, uint64(m.NumBatches))
		i--
		dAtA[i] = 0x28
	}
	if len(m.JobSetId) > 0 {
		i -= len(m.JobSetId)
		copy(dAtA[i:], m.JobSetId)
		i = encodeVarintTestspec(dAtA, i, uint64(len(m.JobSetId)))
		i--
		dAtA[i] = 0x22
	}
	if len(m.Queue) > 0 {
		i -= len(m.Queue)
		copy(dAtA[i:], m.Queue)
		i = encodeVarintTestspec(dAtA, i, uint64(len(m.Queue)))
		i--
		dAtA[i] = 0x1a
	}
	if len(m.ExpectedEvents) > 0 {
		for iNdEx := len(m.ExpectedEvents) - 1; iNdEx >= 0; iNdEx-- {
			{
				size, err := m.ExpectedEvents[iNdEx].MarshalToSizedBuffer(dAtA[:i])
				if err != nil {
					return 0, err
				}
				i -= size
				i = encodeVarintTestspec(dAtA, i, uint64(size))
			}
			i--
			dAtA[i] = 0x12
		}
	}
	if len(m.Jobs) > 0 {
		for iNdEx := len(m.Jobs) - 1; iNdEx >= 0; iNdEx-- {
			{
				size, err := m.Jobs[iNdEx].MarshalToSizedBuffer(dAtA[:i])
				if err != nil {
					return 0, err
				}
				i -= size
				i = encodeVarintTestspec(dAtA, i, uint64(size))
			}
			i--
			dAtA[i] = 0xa
		}
	}
	return len(dAtA) - i, nil
}

func encodeVarintTestspec(dAtA []byte, offset int, v uint64) int {
	offset -= sovTestspec(v)
	base := offset
	for v >= 1<<7 {
		dAtA[offset] = uint8(v&0x7f | 0x80)
		v >>= 7
		offset++
	}
	dAtA[offset] = uint8(v)
	return base
}
func (m *TestSpec) Size() (n int) {
	if m == nil {
		return 0
	}
	var l int
	_ = l
	if len(m.Jobs) > 0 {
		for _, e := range m.Jobs {
			l = e.Size()
			n += 1 + l + sovTestspec(uint64(l))
		}
	}
	if len(m.ExpectedEvents) > 0 {
		for _, e := range m.ExpectedEvents {
			l = e.Size()
			n += 1 + l + sovTestspec(uint64(l))
		}
	}
	l = len(m.Queue)
	if l > 0 {
		n += 1 + l + sovTestspec(uint64(l))
	}
	l = len(m.JobSetId)
	if l > 0 {
		n += 1 + l + sovTestspec(uint64(l))
	}
	if m.NumBatches != 0 {
		n += 1 + sovTestspec(uint64(m.NumBatches))
	}
	if m.BatchSize != 0 {
		n += 1 + sovTestspec(uint64(m.BatchSize))
	}
	l = github_com_gogo_protobuf_types.SizeOfStdDuration(m.Interval)
	n += 1 + l + sovTestspec(uint64(l))
	l = github_com_gogo_protobuf_types.SizeOfStdDuration(m.Timeout)
	n += 1 + l + sovTestspec(uint64(l))
	if m.Cancel != 0 {
		n += 1 + sovTestspec(uint64(m.Cancel))
	}
	l = len(m.Name)
	if l > 0 {
		n += 1 + l + sovTestspec(uint64(l))
	}
	if m.RandomClientId {
		n += 2
	}
	if m.GetLogs {
		n += 2
	}
	return n
}

func sovTestspec(x uint64) (n int) {
	return (math_bits.Len64(x|1) + 6) / 7
}
func sozTestspec(x uint64) (n int) {
	return sovTestspec(uint64((x << 1) ^ uint64((int64(x) >> 63))))
}
func (this *TestSpec) String() string {
	if this == nil {
		return "nil"
	}
	repeatedStringForJobs := "[]*JobSubmitRequestItem{"
	for _, f := range this.Jobs {
		repeatedStringForJobs += strings.Replace(fmt.Sprintf("%v", f), "JobSubmitRequestItem", "JobSubmitRequestItem", 1) + ","
	}
	repeatedStringForJobs += "}"
	repeatedStringForExpectedEvents := "[]*EventMessage{"
	for _, f := range this.ExpectedEvents {
		repeatedStringForExpectedEvents += strings.Replace(fmt.Sprintf("%v", f), "EventMessage", "EventMessage", 1) + ","
	}
	repeatedStringForExpectedEvents += "}"
	s := strings.Join([]string{`&TestSpec{`,
		`Jobs:` + repeatedStringForJobs + `,`,
		`ExpectedEvents:` + repeatedStringForExpectedEvents + `,`,
		`Queue:` + fmt.Sprintf("%v", this.Queue) + `,`,
		`JobSetId:` + fmt.Sprintf("%v", this.JobSetId) + `,`,
		`NumBatches:` + fmt.Sprintf("%v", this.NumBatches) + `,`,
		`BatchSize:` + fmt.Sprintf("%v", this.BatchSize) + `,`,
		`Interval:` + strings.Replace(strings.Replace(fmt.Sprintf("%v", this.Interval), "Duration", "types.Duration", 1), `&`, ``, 1) + `,`,
		`Timeout:` + strings.Replace(strings.Replace(fmt.Sprintf("%v", this.Timeout), "Duration", "types.Duration", 1), `&`, ``, 1) + `,`,
		`Cancel:` + fmt.Sprintf("%v", this.Cancel) + `,`,
		`Name:` + fmt.Sprintf("%v", this.Name) + `,`,
		`RandomClientId:` + fmt.Sprintf("%v", this.RandomClientId) + `,`,
		`GetLogs:` + fmt.Sprintf("%v", this.GetLogs) + `,`,
		`}`,
	}, "")
	return s
}
func valueToStringTestspec(v interface{}) string {
	rv := reflect.ValueOf(v)
	if rv.IsNil() {
		return "nil"
	}
	pv := reflect.Indirect(rv).Interface()
	return fmt.Sprintf("*%v", pv)
}
func (m *TestSpec) Unmarshal(dAtA []byte) error {
	l := len(dAtA)
	iNdEx := 0
	for iNdEx < l {
		preIndex := iNdEx
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return ErrIntOverflowTestspec
			}
			if iNdEx >= l {
				return io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= uint64(b&0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		fieldNum := int32(wire >> 3)
		wireType := int(wire & 0x7)
		if wireType == 4 {
			return fmt.Errorf("proto: TestSpec: wiretype end group for non-group")
		}
		if fieldNum <= 0 {
			return fmt.Errorf("proto: TestSpec: illegal tag %d (wire type %d)", fieldNum, wire)
		}
		switch fieldNum {
		case 1:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Jobs", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthTestspec
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthTestspec
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Jobs = append(m.Jobs, &JobSubmitRequestItem{})
			if err := m.Jobs[len(m.Jobs)-1].Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		case 2:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field ExpectedEvents", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthTestspec
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthTestspec
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.ExpectedEvents = append(m.ExpectedEvents, &EventMessage{})
			if err := m.ExpectedEvents[len(m.ExpectedEvents)-1].Unmarshal(dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		case 3:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Queue", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthTestspec
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthTestspec
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Queue = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 4:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field JobSetId", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthTestspec
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthTestspec
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.JobSetId = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 5:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field NumBatches", wireType)
			}
			m.NumBatches = 0
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				m.NumBatches |= uint32(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
		case 6:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field BatchSize", wireType)
			}
			m.BatchSize = 0
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				m.BatchSize |= uint32(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
		case 7:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Interval", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthTestspec
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthTestspec
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			if err := github_com_gogo_protobuf_types.StdDurationUnmarshal(&m.Interval, dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		case 8:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Timeout", wireType)
			}
			var msglen int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				msglen |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if msglen < 0 {
				return ErrInvalidLengthTestspec
			}
			postIndex := iNdEx + msglen
			if postIndex < 0 {
				return ErrInvalidLengthTestspec
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			if err := github_com_gogo_protobuf_types.StdDurationUnmarshal(&m.Timeout, dAtA[iNdEx:postIndex]); err != nil {
				return err
			}
			iNdEx = postIndex
		case 9:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field Cancel", wireType)
			}
			m.Cancel = 0
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				m.Cancel |= TestSpec_Cancel(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
		case 10:
			if wireType != 2 {
				return fmt.Errorf("proto: wrong wireType = %d for field Name", wireType)
			}
			var stringLen uint64
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				stringLen |= uint64(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			intStringLen := int(stringLen)
			if intStringLen < 0 {
				return ErrInvalidLengthTestspec
			}
			postIndex := iNdEx + intStringLen
			if postIndex < 0 {
				return ErrInvalidLengthTestspec
			}
			if postIndex > l {
				return io.ErrUnexpectedEOF
			}
			m.Name = string(dAtA[iNdEx:postIndex])
			iNdEx = postIndex
		case 11:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field RandomClientId", wireType)
			}
			var v int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				v |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			m.RandomClientId = bool(v != 0)
		case 12:
			if wireType != 0 {
				return fmt.Errorf("proto: wrong wireType = %d for field GetLogs", wireType)
			}
			var v int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				v |= int(b&0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			m.GetLogs = bool(v != 0)
		default:
			iNdEx = preIndex
			skippy, err := skipTestspec(dAtA[iNdEx:])
			if err != nil {
				return err
			}
			if (skippy < 0) || (iNdEx+skippy) < 0 {
				return ErrInvalidLengthTestspec
			}
			if (iNdEx + skippy) > l {
				return io.ErrUnexpectedEOF
			}
			iNdEx += skippy
		}
	}

	if iNdEx > l {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func skipTestspec(dAtA []byte) (n int, err error) {
	l := len(dAtA)
	iNdEx := 0
	depth := 0
	for iNdEx < l {
		var wire uint64
		for shift := uint(0); ; shift += 7 {
			if shift >= 64 {
				return 0, ErrIntOverflowTestspec
			}
			if iNdEx >= l {
				return 0, io.ErrUnexpectedEOF
			}
			b := dAtA[iNdEx]
			iNdEx++
			wire |= (uint64(b) & 0x7F) << shift
			if b < 0x80 {
				break
			}
		}
		wireType := int(wire & 0x7)
		switch wireType {
		case 0:
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				iNdEx++
				if dAtA[iNdEx-1] < 0x80 {
					break
				}
			}
		case 1:
			iNdEx += 8
		case 2:
			var length int
			for shift := uint(0); ; shift += 7 {
				if shift >= 64 {
					return 0, ErrIntOverflowTestspec
				}
				if iNdEx >= l {
					return 0, io.ErrUnexpectedEOF
				}
				b := dAtA[iNdEx]
				iNdEx++
				length |= (int(b) & 0x7F) << shift
				if b < 0x80 {
					break
				}
			}
			if length < 0 {
				return 0, ErrInvalidLengthTestspec
			}
			iNdEx += length
		case 3:
			depth++
		case 4:
			if depth == 0 {
				return 0, ErrUnexpectedEndOfGroupTestspec
			}
			depth--
		case 5:
			iNdEx += 4
		default:
			return 0, fmt.Errorf("proto: illegal wireType %d", wireType)
		}
		if iNdEx < 0 {
			return 0, ErrInvalidLengthTestspec
		}
		if depth == 0 {
			return iNdEx, nil
		}
	}
	return 0, io.ErrUnexpectedEOF
}

var (
	ErrInvalidLengthTestspec        = fmt.Errorf("proto: negative length found during unmarshaling")
	ErrIntOverflowTestspec          = fmt.Errorf("proto: integer overflow")
	ErrUnexpectedEndOfGroupTestspec = fmt.Errorf("proto: unexpected end of group")
)
