// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package jaeger

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/jaegertracing/jaeger/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/consumer/pdata"
	"go.opentelemetry.io/collector/internal/data/testdata"
	"go.opentelemetry.io/collector/translator/conventions"
	tracetranslator "go.opentelemetry.io/collector/translator/trace"
)

// Use timespamp with microsecond granularity to work well with jaeger thrift translation
var (
	testSpanStartTime      = time.Date(2020, 2, 11, 20, 26, 12, 321000, time.UTC)
	testSpanStartTimestamp = pdata.TimestampUnixNano(testSpanStartTime.UnixNano())
	testSpanEventTime      = time.Date(2020, 2, 11, 20, 26, 13, 123000, time.UTC)
	testSpanEventTimestamp = pdata.TimestampUnixNano(testSpanEventTime.UnixNano())
	testSpanEndTime        = time.Date(2020, 2, 11, 20, 26, 13, 789000, time.UTC)
	testSpanEndTimestamp   = pdata.TimestampUnixNano(testSpanEndTime.UnixNano())
)

func TestGetStatusCodeFromAttr(t *testing.T) {
	_, invalidNumErr := strconv.Atoi("inf")

	tests := []struct {
		name string
		attr pdata.AttributeValue
		code pdata.StatusCode
		err  error
	}{
		{
			name: "ok-string",
			attr: pdata.NewAttributeValueString("0"),
			code: pdata.StatusCodeOk,
			err:  nil,
		},

		{
			name: "ok-int",
			attr: pdata.NewAttributeValueInt(1),
			code: pdata.StatusCodeCancelled,
			err:  nil,
		},

		{
			name: "wrong-type",
			attr: pdata.NewAttributeValueBool(true),
			code: pdata.StatusCodeOk,
			err:  fmt.Errorf("invalid status code attribute type: BOOL"),
		},

		{
			name: "invalid-string",
			attr: pdata.NewAttributeValueString("inf"),
			code: pdata.StatusCodeOk,
			err:  invalidNumErr,
		},

		{
			name: "invalid-int",
			attr: pdata.NewAttributeValueInt(1844674407370955),
			code: pdata.StatusCodeOk,
			err:  fmt.Errorf("invalid status code value: 1844674407370955"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, err := getStatusCodeFromAttr(test.attr)
			assert.EqualValues(t, test.err, err)
			assert.Equal(t, test.code, code)
		})
	}
}

func TestGetStatusCodeFromHTTPStatusAttr(t *testing.T) {
	tests := []struct {
		name string
		attr pdata.AttributeValue
		code pdata.StatusCode
	}{
		{
			name: "string-unknown",
			attr: pdata.NewAttributeValueString("10"),
			code: pdata.StatusCodeUnknownError,
		},

		{
			name: "string-ok",
			attr: pdata.NewAttributeValueString("101"),
			code: pdata.StatusCodeOk,
		},

		{
			name: "int-not-found",
			attr: pdata.NewAttributeValueInt(404),
			code: pdata.StatusCodeNotFound,
		},
		{
			name: "int-invalid-arg",
			attr: pdata.NewAttributeValueInt(408),
			code: pdata.StatusCodeInvalidArgument,
		},

		{
			name: "int-internal",
			attr: pdata.NewAttributeValueInt(500),
			code: pdata.StatusCodeInternalError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, err := getStatusCodeFromHTTPStatusAttr(test.attr)
			assert.NoError(t, err)
			assert.Equal(t, test.code, code)
		})
	}
}

func TestJTagsToInternalAttributes(t *testing.T) {
	tags := []model.KeyValue{
		{
			Key:   "bool-val",
			VType: model.ValueType_BOOL,
			VBool: true,
		},
		{
			Key:    "int-val",
			VType:  model.ValueType_INT64,
			VInt64: 123,
		},
		{
			Key:   "string-val",
			VType: model.ValueType_STRING,
			VStr:  "abc",
		},
		{
			Key:      "double-val",
			VType:    model.ValueType_FLOAT64,
			VFloat64: 1.23,
		},
		{
			Key:     "binary-val",
			VType:   model.ValueType_BINARY,
			VBinary: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x64, 0x7D, 0x98},
		},
	}

	expected := pdata.NewAttributeMap()
	expected.InsertBool("bool-val", true)
	expected.InsertInt("int-val", 123)
	expected.InsertString("string-val", "abc")
	expected.InsertDouble("double-val", 1.23)
	expected.InsertString("binary-val", "AAAAAABkfZg=")

	got := pdata.NewAttributeMap()
	jTagsToInternalAttributes(tags, got)

	require.EqualValues(t, expected, got)
}

func TestProtoBatchToInternalTraces(t *testing.T) {

	tests := []struct {
		name string
		jb   model.Batch
		td   pdata.Traces
	}{
		{
			name: "empty",
			jb:   model.Batch{},
			td:   testdata.GenerateTraceDataEmpty(),
		},

		{
			name: "no-spans",
			jb: model.Batch{
				Process: generateProtoProcess(),
			},
			td: generateTraceDataResourceOnly(),
		},

		{
			name: "no-resource-attrs",
			jb: model.Batch{
				Process: &model.Process{
					ServiceName: tracetranslator.ResourceNoAttrs,
				},
			},
			td: generateTraceDataResourceOnlyWithNoAttrs(),
		},

		{
			name: "one-span-no-resources",
			jb: model.Batch{
				Process: &model.Process{
					ServiceName: tracetranslator.ResourceNotSet,
				},
				Spans: []*model.Span{
					generateProtoSpanWithTraceState(),
				},
			},
			td: generateTraceDataOneSpanNoResourceWithTraceState(),
		},
		{
			name: "two-spans-child-parent",
			jb: model.Batch{
				Process: &model.Process{
					ServiceName: tracetranslator.ResourceNotSet,
				},
				Spans: []*model.Span{
					generateProtoSpan(),
					generateProtoChildSpan(),
				},
			},
			td: generateTraceDataTwoSpansChildParent(),
		},

		{
			name: "two-spans-with-follower",
			jb: model.Batch{
				Process: &model.Process{
					ServiceName: tracetranslator.ResourceNotSet,
				},
				Spans: []*model.Span{
					generateProtoSpan(),
					generateProtoFollowerSpan(),
				},
			},
			td: generateTraceDataTwoSpansWithFollower(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			td := ProtoBatchToInternalTraces(test.jb)
			assert.EqualValues(t, test.td, td)
		})
	}
}

func TestSetInternalSpanStatus(t *testing.T) {

	nilStatus := pdata.NewSpanStatus()

	okStatus := pdata.NewSpanStatus()
	okStatus.InitEmpty()
	okStatus.SetCode(pdata.StatusCodeOk)

	unknownStatus := pdata.NewSpanStatus()
	unknownStatus.InitEmpty()
	unknownStatus.SetCode(pdata.StatusCodeUnknownError)

	canceledStatus := pdata.NewSpanStatus()
	canceledStatus.InitEmpty()
	canceledStatus.SetCode(pdata.StatusCodeCancelled)

	invalidStatusWithMessage := pdata.NewSpanStatus()
	invalidStatusWithMessage.InitEmpty()
	invalidStatusWithMessage.SetCode(pdata.StatusCodeInvalidArgument)
	invalidStatusWithMessage.SetMessage("Error: Invalid argument")

	notFoundStatus := pdata.NewSpanStatus()
	notFoundStatus.InitEmpty()
	notFoundStatus.SetCode(pdata.StatusCodeNotFound)

	notFoundStatusWithMessage := pdata.NewSpanStatus()
	notFoundStatusWithMessage.InitEmpty()
	notFoundStatusWithMessage.SetCode(pdata.StatusCodeNotFound)
	notFoundStatusWithMessage.SetMessage("HTTP 404: Not Found")

	tests := []struct {
		name             string
		attrs            pdata.AttributeMap
		status           pdata.SpanStatus
		attrsModifiedLen int // Length of attributes map after dropping converted fields
	}{
		{
			name:             "No tags set -> OK status",
			attrs:            pdata.NewAttributeMap().InitFromMap(map[string]pdata.AttributeValue{}),
			status:           nilStatus,
			attrsModifiedLen: 0,
		},
		{
			name: "error tag set -> Unknown status",
			attrs: pdata.NewAttributeMap().InitFromMap(map[string]pdata.AttributeValue{
				tracetranslator.TagError: pdata.NewAttributeValueBool(true),
			}),
			status:           unknownStatus,
			attrsModifiedLen: 0,
		},
		{
			name: "status.code is set as int",
			attrs: pdata.NewAttributeMap().InitFromMap(map[string]pdata.AttributeValue{
				tracetranslator.TagStatusCode: pdata.NewAttributeValueInt(1),
			}),
			status:           canceledStatus,
			attrsModifiedLen: 0,
		},
		{
			name: "status.code, status.message and error tags are set",
			attrs: pdata.NewAttributeMap().InitFromMap(map[string]pdata.AttributeValue{
				tracetranslator.TagError:      pdata.NewAttributeValueBool(true),
				tracetranslator.TagStatusCode: pdata.NewAttributeValueInt(3),
				tracetranslator.TagStatusMsg:  pdata.NewAttributeValueString("Error: Invalid argument"),
			}),
			status:           invalidStatusWithMessage,
			attrsModifiedLen: 0,
		},
		{
			name: "http.status_code tag is set as string",
			attrs: pdata.NewAttributeMap().InitFromMap(map[string]pdata.AttributeValue{
				tracetranslator.TagHTTPStatusCode: pdata.NewAttributeValueString("404"),
			}),
			status:           notFoundStatus,
			attrsModifiedLen: 1,
		},
		{
			name: "http.status_code, http.status_message and error tags are set",
			attrs: pdata.NewAttributeMap().InitFromMap(map[string]pdata.AttributeValue{
				tracetranslator.TagError:          pdata.NewAttributeValueBool(true),
				tracetranslator.TagHTTPStatusCode: pdata.NewAttributeValueInt(404),
				tracetranslator.TagHTTPStatusMsg:  pdata.NewAttributeValueString("HTTP 404: Not Found"),
			}),
			status:           notFoundStatusWithMessage,
			attrsModifiedLen: 2,
		},
		{
			name: "status.code has precedence over http.status_code.",
			attrs: pdata.NewAttributeMap().InitFromMap(map[string]pdata.AttributeValue{
				tracetranslator.TagStatusCode:     pdata.NewAttributeValueInt(1),
				tracetranslator.TagHTTPStatusCode: pdata.NewAttributeValueInt(500),
				tracetranslator.TagHTTPStatusMsg:  pdata.NewAttributeValueString("Server Error"),
			}),
			status:           canceledStatus,
			attrsModifiedLen: 2,
		},
		{
			name: "Ignore http.status_code == 200 if error set to true.",
			attrs: pdata.NewAttributeMap().InitFromMap(map[string]pdata.AttributeValue{
				tracetranslator.TagError:          pdata.NewAttributeValueBool(true),
				tracetranslator.TagHTTPStatusCode: pdata.NewAttributeValueInt(200),
			}),
			status:           unknownStatus,
			attrsModifiedLen: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := pdata.NewSpanStatus()
			setInternalSpanStatus(test.attrs, status)
			assert.EqualValues(t, test.status, status)
			assert.Equal(t, test.attrsModifiedLen, test.attrs.Len())
		})
	}
}

func TestProtoBatchesToInternalTraces(t *testing.T) {
	batches := []*model.Batch{
		{
			Process: generateProtoProcess(),
			Spans: []*model.Span{
				generateProtoSpan(),
			},
		},
		{
			Spans: []*model.Span{
				generateProtoSpan(),
				generateProtoChildSpan(),
			},
		},
		{
			// should be skipped
			Spans: []*model.Span{},
		},
	}

	expected := generateTraceDataOneSpanNoResource()
	resource := generateTraceDataResourceOnly().ResourceSpans().At(0).Resource()
	resource.CopyTo(expected.ResourceSpans().At(0).Resource())
	expected.ResourceSpans().Resize(2)
	twoSpans := generateTraceDataTwoSpansChildParent().ResourceSpans().At(0)
	twoSpans.CopyTo(expected.ResourceSpans().At(1))

	got := ProtoBatchesToInternalTraces(batches)
	assert.EqualValues(t, expected, got)
}

func TestJSpanKindToInternal(t *testing.T) {
	tests := []struct {
		jSpanKind    string
		otlpSpanKind pdata.SpanKind
	}{
		{
			jSpanKind:    "client",
			otlpSpanKind: pdata.SpanKindCLIENT,
		},
		{
			jSpanKind:    "server",
			otlpSpanKind: pdata.SpanKindSERVER,
		},
		{
			jSpanKind:    "producer",
			otlpSpanKind: pdata.SpanKindPRODUCER,
		},
		{
			jSpanKind:    "consumer",
			otlpSpanKind: pdata.SpanKindCONSUMER,
		},
		{
			jSpanKind:    "internal",
			otlpSpanKind: pdata.SpanKindINTERNAL,
		},
		{
			jSpanKind:    "all-others",
			otlpSpanKind: pdata.SpanKindUNSPECIFIED,
		},
	}

	for _, test := range tests {
		t.Run(test.jSpanKind, func(t *testing.T) {
			assert.Equal(t, test.otlpSpanKind, jSpanKindToInternal(test.jSpanKind))
		})
	}
}

func generateTraceDataResourceOnly() pdata.Traces {
	td := testdata.GenerateTraceDataOneEmptyResourceSpans()
	rs := td.ResourceSpans().At(0).Resource()
	rs.InitEmpty()
	rs.Attributes().InsertString(conventions.AttributeServiceName, "service-1")
	rs.Attributes().InsertInt("int-attr-1", 123)
	return td
}

func generateTraceDataResourceOnlyWithNoAttrs() pdata.Traces {
	td := testdata.GenerateTraceDataOneEmptyResourceSpans()
	rs := td.ResourceSpans().At(0).Resource()
	rs.InitEmpty()
	return td
}

func generateProtoProcess() *model.Process {
	return &model.Process{
		ServiceName: "service-1",
		Tags: []model.KeyValue{
			{
				Key:    "int-attr-1",
				VType:  model.ValueType_INT64,
				VInt64: 123,
			},
		},
	}
}

func generateTraceDataOneSpanNoResource() pdata.Traces {
	td := testdata.GenerateTraceDataOneSpanNoResource()
	span := td.ResourceSpans().At(0).InstrumentationLibrarySpans().At(0).Spans().At(0)
	span.SetSpanID(pdata.NewSpanID([]byte{0xAF, 0xAE, 0xAD, 0xAC, 0xAB, 0xAA, 0xA9, 0xA8}))
	span.SetTraceID(pdata.NewTraceID(
		[]byte{0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8, 0xF9, 0xFA, 0xFB, 0xFC, 0xFD, 0xFE, 0xFF, 0x80}))
	span.SetDroppedAttributesCount(0)
	span.SetDroppedEventsCount(0)
	span.SetStartTime(testSpanStartTimestamp)
	span.SetEndTime(testSpanEndTimestamp)
	span.SetKind(pdata.SpanKindCLIENT)
	span.Events().At(0).SetTimestamp(testSpanEventTimestamp)
	span.Events().At(0).SetDroppedAttributesCount(0)
	span.Events().At(0).SetName("event-with-attr")
	span.Events().At(1).SetTimestamp(testSpanEventTimestamp)
	span.Events().At(1).SetDroppedAttributesCount(0)
	span.Events().At(1).SetName("")
	span.Events().At(1).Attributes().InsertInt("attr-int", 123)
	return td
}

func generateTraceDataOneSpanNoResourceWithTraceState() pdata.Traces {
	td := generateTraceDataOneSpanNoResource()
	span := td.ResourceSpans().At(0).InstrumentationLibrarySpans().At(0).Spans().At(0)
	span.SetTraceState("lasterror=f39cd56cc44274fd5abd07ef1164246d10ce2955")
	return td
}

func generateProtoSpan() *model.Span {
	return &model.Span{
		TraceID: model.NewTraceID(
			binary.BigEndian.Uint64([]byte{0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8}),
			binary.BigEndian.Uint64([]byte{0xF9, 0xFA, 0xFB, 0xFC, 0xFD, 0xFE, 0xFF, 0x80}),
		),
		SpanID:        model.NewSpanID(binary.BigEndian.Uint64([]byte{0xAF, 0xAE, 0xAD, 0xAC, 0xAB, 0xAA, 0xA9, 0xA8})),
		OperationName: "operationA",
		StartTime:     testSpanStartTime,
		Duration:      testSpanEndTime.Sub(testSpanStartTime),
		Logs: []model.Log{
			{
				Timestamp: testSpanEventTime,
				Fields: []model.KeyValue{
					{
						Key:   tracetranslator.TagMessage,
						VType: model.ValueType_STRING,
						VStr:  "event-with-attr",
					},
					{
						Key:   "span-event-attr",
						VType: model.ValueType_STRING,
						VStr:  "span-event-attr-val",
					},
				},
			},
			{
				Timestamp: testSpanEventTime,
				Fields: []model.KeyValue{
					{
						Key:    "attr-int",
						VType:  model.ValueType_INT64,
						VInt64: 123,
					},
				},
			},
		},
		Tags: []model.KeyValue{
			{
				Key:   tracetranslator.TagSpanKind,
				VType: model.ValueType_STRING,
				VStr:  string(tracetranslator.OpenTracingSpanKindClient),
			},
			{
				Key:    tracetranslator.TagStatusCode,
				VType:  model.ValueType_INT64,
				VInt64: tracetranslator.OCCancelled,
			},
			{
				Key:   tracetranslator.TagError,
				VBool: true,
				VType: model.ValueType_BOOL,
			},
			{
				Key:   tracetranslator.TagStatusMsg,
				VType: model.ValueType_STRING,
				VStr:  "status-cancelled",
			},
		},
	}
}

func generateProtoSpanWithTraceState() *model.Span {
	return &model.Span{
		TraceID: model.NewTraceID(
			binary.BigEndian.Uint64([]byte{0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8}),
			binary.BigEndian.Uint64([]byte{0xF9, 0xFA, 0xFB, 0xFC, 0xFD, 0xFE, 0xFF, 0x80}),
		),
		SpanID:        model.NewSpanID(binary.BigEndian.Uint64([]byte{0xAF, 0xAE, 0xAD, 0xAC, 0xAB, 0xAA, 0xA9, 0xA8})),
		OperationName: "operationA",
		StartTime:     testSpanStartTime,
		Duration:      testSpanEndTime.Sub(testSpanStartTime),
		Logs: []model.Log{
			{
				Timestamp: testSpanEventTime,
				Fields: []model.KeyValue{
					{
						Key:   tracetranslator.TagMessage,
						VType: model.ValueType_STRING,
						VStr:  "event-with-attr",
					},
					{
						Key:   "span-event-attr",
						VType: model.ValueType_STRING,
						VStr:  "span-event-attr-val",
					},
				},
			},
			{
				Timestamp: testSpanEventTime,
				Fields: []model.KeyValue{
					{
						Key:    "attr-int",
						VType:  model.ValueType_INT64,
						VInt64: 123,
					},
				},
			},
		},
		Tags: []model.KeyValue{
			{
				Key:   tracetranslator.TagSpanKind,
				VType: model.ValueType_STRING,
				VStr:  string(tracetranslator.OpenTracingSpanKindClient),
			},
			{
				Key:    tracetranslator.TagStatusCode,
				VType:  model.ValueType_INT64,
				VInt64: tracetranslator.OCCancelled,
			},
			{
				Key:   tracetranslator.TagError,
				VBool: true,
				VType: model.ValueType_BOOL,
			},
			{
				Key:   tracetranslator.TagStatusMsg,
				VType: model.ValueType_STRING,
				VStr:  "status-cancelled",
			},
			{
				Key:   tracetranslator.TagW3CTraceState,
				VType: model.ValueType_STRING,
				VStr:  "lasterror=f39cd56cc44274fd5abd07ef1164246d10ce2955",
			},
		},
	}
}

func generateTraceDataTwoSpansChildParent() pdata.Traces {
	td := generateTraceDataOneSpanNoResource()
	spans := td.ResourceSpans().At(0).InstrumentationLibrarySpans().At(0).Spans()
	spans.Resize(2)

	span := spans.At(1)
	span.SetName("operationB")
	span.SetSpanID([]byte{0x1F, 0x1E, 0x1D, 0x1C, 0x1B, 0x1A, 0x19, 0x18})
	span.SetParentSpanID(spans.At(0).SpanID())
	span.SetKind(pdata.SpanKindSERVER)
	span.SetTraceID(spans.At(0).TraceID())
	span.SetStartTime(spans.At(0).StartTime())
	span.SetEndTime(spans.At(0).EndTime())
	span.Status().InitEmpty()
	span.Status().SetCode(pdata.StatusCodeNotFound)
	span.Attributes().InitFromMap(map[string]pdata.AttributeValue{
		tracetranslator.TagHTTPStatusCode: pdata.NewAttributeValueInt(404),
	})

	return td
}

func generateProtoChildSpan() *model.Span {
	traceID := model.NewTraceID(
		binary.BigEndian.Uint64([]byte{0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8}),
		binary.BigEndian.Uint64([]byte{0xF9, 0xFA, 0xFB, 0xFC, 0xFD, 0xFE, 0xFF, 0x80}),
	)
	return &model.Span{
		TraceID:       traceID,
		SpanID:        model.NewSpanID(binary.BigEndian.Uint64([]byte{0x1F, 0x1E, 0x1D, 0x1C, 0x1B, 0x1A, 0x19, 0x18})),
		OperationName: "operationB",
		StartTime:     testSpanStartTime,
		Duration:      testSpanEndTime.Sub(testSpanStartTime),
		Tags: []model.KeyValue{
			{
				Key:    tracetranslator.TagHTTPStatusCode,
				VType:  model.ValueType_INT64,
				VInt64: 404,
			},
			{
				Key:   tracetranslator.TagSpanKind,
				VType: model.ValueType_STRING,
				VStr:  string(tracetranslator.OpenTracingSpanKindServer),
			},
		},
		References: []model.SpanRef{
			{
				TraceID: traceID,
				SpanID:  model.NewSpanID(binary.BigEndian.Uint64([]byte{0xAF, 0xAE, 0xAD, 0xAC, 0xAB, 0xAA, 0xA9, 0xA8})),
				RefType: model.SpanRefType_CHILD_OF,
			},
		},
	}
}

func generateTraceDataTwoSpansWithFollower() pdata.Traces {
	td := generateTraceDataOneSpanNoResource()
	spans := td.ResourceSpans().At(0).InstrumentationLibrarySpans().At(0).Spans()
	spans.Resize(2)

	span := spans.At(1)
	span.SetName("operationC")
	span.SetSpanID([]byte{0x1F, 0x1E, 0x1D, 0x1C, 0x1B, 0x1A, 0x19, 0x18})
	span.SetTraceID(spans.At(0).TraceID())
	span.SetStartTime(spans.At(0).EndTime())
	span.SetEndTime(spans.At(0).EndTime() + 1000000)
	span.SetKind(pdata.SpanKindCONSUMER)
	span.Status().InitEmpty()
	span.Status().SetCode(pdata.StatusCodeOk)
	span.Status().SetMessage("status-ok")
	span.Links().Resize(1)
	span.Links().At(0).SetTraceID(span.TraceID())
	span.Links().At(0).SetSpanID(spans.At(0).SpanID())
	return td
}

func generateProtoFollowerSpan() *model.Span {
	traceID := model.NewTraceID(
		binary.BigEndian.Uint64([]byte{0xF1, 0xF2, 0xF3, 0xF4, 0xF5, 0xF6, 0xF7, 0xF8}),
		binary.BigEndian.Uint64([]byte{0xF9, 0xFA, 0xFB, 0xFC, 0xFD, 0xFE, 0xFF, 0x80}),
	)
	return &model.Span{
		TraceID:       traceID,
		SpanID:        model.NewSpanID(binary.BigEndian.Uint64([]byte{0x1F, 0x1E, 0x1D, 0x1C, 0x1B, 0x1A, 0x19, 0x18})),
		OperationName: "operationC",
		StartTime:     testSpanEndTime,
		Duration:      time.Millisecond,
		Tags: []model.KeyValue{
			{
				Key:   tracetranslator.TagSpanKind,
				VType: model.ValueType_STRING,
				VStr:  string(tracetranslator.OpenTracingSpanKindConsumer),
			},
			{
				Key:    tracetranslator.TagStatusCode,
				VType:  model.ValueType_INT64,
				VInt64: tracetranslator.OCOK,
			},
			{
				Key:   tracetranslator.TagStatusMsg,
				VType: model.ValueType_STRING,
				VStr:  "status-ok",
			},
		},
		References: []model.SpanRef{
			{
				TraceID: traceID,
				SpanID:  model.NewSpanID(binary.BigEndian.Uint64([]byte{0xAF, 0xAE, 0xAD, 0xAC, 0xAB, 0xAA, 0xA9, 0xA8})),
				RefType: model.SpanRefType_FOLLOWS_FROM,
			},
		},
	}
}

func BenchmarkProtoBatchToInternalTraces(b *testing.B) {
	jb := model.Batch{
		Process: generateProtoProcess(),
		Spans: []*model.Span{
			generateProtoSpan(),
			generateProtoChildSpan(),
		},
	}

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		ProtoBatchToInternalTraces(jb)
	}
}
