package loadmodel

import "testing"

func TestSelectCommandLogLoadKafkaTopics(t *testing.T) {
	topics, err := SelectCommandLogLoadKafkaTopics([]string{
		"budgie.commands.load.2",
		"budgie.events.load.1",
		"budgie.commands.production",
		"budgie.commands.load.2",
		" other.topic ",
		"",
	}, CommandLogLoadKafkaCommandTopicPrefix, CommandLogLoadKafkaEventTopicPrefix)
	if err != nil {
		t.Fatalf("select load topics: %v", err)
	}
	requireSamples(t, "selected topics", topics, "budgie.commands.load.2", "budgie.events.load.1")

	_, err = SelectCommandLogLoadKafkaTopics([]string{"anything"}, "", CommandLogLoadKafkaEventTopicPrefix)
	requireErrorContains(t, err, "prefixes must be non-empty")
	_, err = SelectCommandLogLoadKafkaTopics([]string{"anything"}, "budgie.load.", "budgie.load.")
	requireErrorContains(t, err, "prefixes must be distinct")
}
