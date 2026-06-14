package kafkaconn

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestEnsureTopicsWithRequestorCreatesAndVerifiesTopics(t *testing.T) {
	requestor := &fakeKafkaTopicRequestor{
		responses: []kmsg.Response{
			createTopicsResponse(
				createTopicResult("budgie.commands.load.1", 0, ""),
				createTopicResult("budgie.events.load.1", kerr.TopicAlreadyExists.Code, ""),
			),
			metadataResponse(
				metadataTopic("budgie.commands.load.1", 0, 32),
				metadataTopic("budgie.events.load.1", 0, 48),
			),
		},
	}
	specs := []TopicProvisioningSpec{
		{Topic: " budgie.commands.load.1 ", Partitions: 32, ReplicationFactor: 3},
		{Topic: "budgie.events.load.1", Partitions: 48},
	}

	if err := ensureTopicsWithRequestor(context.Background(), requestor, specs, time.Second); err != nil {
		t.Fatalf("ensure topics: %v", err)
	}
	if len(requestor.requests) != 2 {
		t.Fatalf("request count = %d, want create plus metadata", len(requestor.requests))
	}
	createReq, ok := requestor.requests[0].(*kmsg.CreateTopicsRequest)
	if !ok {
		t.Fatalf("first request = %T, want CreateTopicsRequest", requestor.requests[0])
	}
	if len(createReq.Topics) != 2 {
		t.Fatalf("create topics = %d, want 2", len(createReq.Topics))
	}
	if createReq.Topics[0].Topic != "budgie.commands.load.1" ||
		createReq.Topics[0].NumPartitions != 32 ||
		createReq.Topics[0].ReplicationFactor != 3 {
		t.Fatalf("command create request = %+v, want trimmed topic, 32 partitions, rf=3", createReq.Topics[0])
	}
	if createReq.Topics[1].ReplicationFactor != 1 {
		t.Fatalf("event create replication factor = %d, want default 1", createReq.Topics[1].ReplicationFactor)
	}
	metadataReq, ok := requestor.requests[1].(*kmsg.MetadataRequest)
	if !ok {
		t.Fatalf("second request = %T, want MetadataRequest", requestor.requests[1])
	}
	if metadataReq.AllowAutoTopicCreation {
		t.Fatalf("metadata request allowed auto topic creation; want disabled")
	}
}

func TestEnsureTopicsWithRequestorRejectsUnderPartitionedExistingTopic(t *testing.T) {
	requestor := &fakeKafkaTopicRequestor{
		responses: []kmsg.Response{
			createTopicsResponse(createTopicResult("budgie.commands.load.1", kerr.TopicAlreadyExists.Code, "")),
			metadataResponse(metadataTopic("budgie.commands.load.1", 0, 8)),
		},
	}

	err := ensureTopicsWithRequestor(context.Background(), requestor, []TopicProvisioningSpec{
		{Topic: "budgie.commands.load.1", Partitions: 32, ReplicationFactor: 1},
	}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "has 8 partitions, requires at least 32") {
		t.Fatalf("ensure under-partitioned topic err = %v, want partition-floor error", err)
	}
}

func TestEnsureTopicsWithRequestorRejectsCreateErrors(t *testing.T) {
	requestor := &fakeKafkaTopicRequestor{
		responses: []kmsg.Response{
			createTopicsResponse(createTopicResult("budgie.commands.load.1", kerr.TopicAuthorizationFailed.Code, "denied")),
		},
	}

	err := ensureTopicsWithRequestor(context.Background(), requestor, []TopicProvisioningSpec{
		{Topic: "budgie.commands.load.1", Partitions: 32, ReplicationFactor: 1},
	}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "TOPIC_AUTHORIZATION_FAILED") || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("ensure unauthorized topic err = %v, want create error with broker message", err)
	}
}

func TestEnsureTopicsWithRequestorValidatesSpecsBeforeRequests(t *testing.T) {
	requestor := &fakeKafkaTopicRequestor{}

	err := ensureTopicsWithRequestor(context.Background(), requestor, []TopicProvisioningSpec{
		{Topic: "", Partitions: 32},
	}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "topic name is required") {
		t.Fatalf("empty topic err = %v, want topic-name validation", err)
	}
	err = ensureTopicsWithRequestor(context.Background(), requestor, []TopicProvisioningSpec{
		{Topic: "budgie.commands.load.1", Partitions: 0},
	}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "positive partition count") {
		t.Fatalf("zero partitions err = %v, want partition validation", err)
	}
	err = ensureTopicsWithRequestor(context.Background(), requestor, []TopicProvisioningSpec{
		{Topic: "budgie.commands.load.1", Partitions: 32},
		{Topic: "budgie.commands.load.1", Partitions: 32},
	}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "duplicate topic") {
		t.Fatalf("duplicate topic err = %v, want duplicate-topic validation", err)
	}
	if len(requestor.requests) != 0 {
		t.Fatalf("request count = %d, want no requests after validation errors", len(requestor.requests))
	}
}

type fakeKafkaTopicRequestor struct {
	requests  []kmsg.Request
	responses []kmsg.Response
	errors    []error
}

func (f *fakeKafkaTopicRequestor) Request(_ context.Context, req kmsg.Request) (kmsg.Response, error) {
	f.requests = append(f.requests, req)
	if len(f.errors) > 0 {
		err := f.errors[0]
		f.errors = f.errors[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(f.responses) == 0 {
		return nil, errors.New("unexpected request")
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func createTopicsResponse(topics ...kmsg.CreateTopicsResponseTopic) *kmsg.CreateTopicsResponse {
	resp := kmsg.NewPtrCreateTopicsResponse()
	resp.Topics = append(resp.Topics, topics...)
	return resp
}

func createTopicResult(topic string, code int16, message string) kmsg.CreateTopicsResponseTopic {
	out := kmsg.NewCreateTopicsResponseTopic()
	out.Topic = topic
	out.ErrorCode = code
	if message != "" {
		out.ErrorMessage = kmsg.StringPtr(message)
	}
	return out
}

func metadataResponse(topics ...kmsg.MetadataResponseTopic) *kmsg.MetadataResponse {
	resp := kmsg.NewPtrMetadataResponse()
	resp.Topics = append(resp.Topics, topics...)
	return resp
}

func metadataTopic(topic string, code int16, partitions int) kmsg.MetadataResponseTopic {
	out := kmsg.NewMetadataResponseTopic()
	out.Topic = kmsg.StringPtr(topic)
	out.ErrorCode = code
	for i := 0; i < partitions; i++ {
		partition := kmsg.NewMetadataResponseTopicPartition()
		partition.Partition = int32(i)
		out.Partitions = append(out.Partitions, partition)
	}
	return out
}
