package kafkaconn

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func TestListTopicsWithRequestorSortsAndDisablesAutoCreate(t *testing.T) {
	requestor := &fakeKafkaTopicRequestor{
		responses: []kmsg.Response{
			metadataResponse(
				metadataTopic("z-topic", 0, 1),
				metadataTopic("budgie.commands.load.1", 0, 32),
				metadataTopic("budgie.events.load.1", 0, 32),
			),
		},
	}

	topics, err := listTopicsWithRequestor(context.Background(), requestor, time.Second)
	if err != nil {
		t.Fatalf("list topics: %v", err)
	}
	want := []string{"budgie.commands.load.1", "budgie.events.load.1", "z-topic"}
	if strings.Join(topics, ",") != strings.Join(want, ",") {
		t.Fatalf("topics = %+v, want sorted %+v", topics, want)
	}
	req, ok := requestor.requests[0].(*kmsg.MetadataRequest)
	if !ok {
		t.Fatalf("request = %T, want MetadataRequest", requestor.requests[0])
	}
	if req.Topics != nil {
		t.Fatalf("metadata request topics = %+v, want nil to list all topics", req.Topics)
	}
	if req.AllowAutoTopicCreation {
		t.Fatalf("metadata request allowed auto topic creation; want disabled")
	}
}

func TestDeleteTopicsWithRequestorDeletesNamesAndIgnoresMissingWhenRequested(t *testing.T) {
	requestor := &fakeKafkaTopicRequestor{
		responses: []kmsg.Response{
			deleteTopicsResponse(
				deleteTopicResult("budgie.commands.load.1", 0, ""),
				deleteTopicResult("budgie.events.load.1", kerr.UnknownTopicOrPartition.Code, ""),
			),
		},
	}

	err := deleteTopicsWithRequestor(context.Background(), requestor, []string{
		" budgie.events.load.1 ",
		"budgie.commands.load.1",
		"budgie.commands.load.1",
	}, time.Second, true)
	if err != nil {
		t.Fatalf("delete topics: %v", err)
	}
	req, ok := requestor.requests[0].(*kmsg.DeleteTopicsRequest)
	if !ok {
		t.Fatalf("request = %T, want DeleteTopicsRequest", requestor.requests[0])
	}
	want := []string{"budgie.commands.load.1", "budgie.events.load.1"}
	if strings.Join(req.TopicNames, ",") != strings.Join(want, ",") {
		t.Fatalf("topic names = %+v, want sorted/deduped %+v", req.TopicNames, want)
	}
	if len(req.Topics) != 2 || req.Topics[0].Topic == nil || *req.Topics[0].Topic != "budgie.commands.load.1" {
		t.Fatalf("v6 delete topics = %+v, want populated topic-name structs", req.Topics)
	}
}

func TestDeleteTopicsWithRequestorRejectsDeleteErrors(t *testing.T) {
	requestor := &fakeKafkaTopicRequestor{
		responses: []kmsg.Response{
			deleteTopicsResponse(deleteTopicResult("budgie.commands.load.1", kerr.TopicAuthorizationFailed.Code, "denied")),
		},
	}

	err := deleteTopicsWithRequestor(context.Background(), requestor, []string{"budgie.commands.load.1"}, time.Second, false)
	requireErrorContains(t, err, "TOPIC_AUTHORIZATION_FAILED", "denied")
}

func TestDeleteTopicsWithRequestorValidatesNamesBeforeRequests(t *testing.T) {
	requestor := &fakeKafkaTopicRequestor{}

	err := deleteTopicsWithRequestor(context.Background(), requestor, []string{" "}, time.Second, true)
	requireErrorContains(t, err, "topic name is required")
	if len(requestor.requests) != 0 {
		t.Fatalf("request count = %d, want no request after validation error", len(requestor.requests))
	}
}

func deleteTopicsResponse(topics ...kmsg.DeleteTopicsResponseTopic) *kmsg.DeleteTopicsResponse {
	resp := kmsg.NewPtrDeleteTopicsResponse()
	resp.Topics = append(resp.Topics, topics...)
	return resp
}

func deleteTopicResult(topic string, code int16, message string) kmsg.DeleteTopicsResponseTopic {
	out := kmsg.NewDeleteTopicsResponseTopic()
	out.Topic = kmsg.StringPtr(topic)
	out.ErrorCode = code
	if message != "" {
		out.ErrorMessage = kmsg.StringPtr(message)
	}
	return out
}
