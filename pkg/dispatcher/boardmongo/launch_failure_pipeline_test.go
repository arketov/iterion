package boardmongo

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/arketov/iterion/pkg/dispatcher/native"
)

// RecordLaunchFailure is an atomic APPEND pipeline: it must $set only
// issue.launchfailures (never issue.updatedat — a stale snapshot value would
// roll the card's dispatch ordering back) and cap the array at
// MaxLaunchFailures with a negative $slice (keep the newest).
func TestLaunchFailureAppendPipeline_Shape(t *testing.T) {
	pipe, err := launchFailureAppendPipeline(native.LaunchFailure{Attempt: 1, Hook: "after_create", Error: "$ boom"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pipe) != 1 {
		t.Fatalf("pipeline has %d stages, want 1", len(pipe))
	}
	set, ok := pipe[0][0].Value.(bson.M)
	if !ok || pipe[0][0].Key != "$set" {
		t.Fatalf("stage = %v, want a $set document", pipe[0])
	}
	if len(set) != 1 {
		t.Fatalf("$set carries %d keys (%v), want only issue.launchfailures", len(set), set)
	}
	if _, bad := set["issue.updatedat"]; bad {
		t.Fatal("$set writes issue.updatedat — a launch failure must not bump or roll back UpdatedAt")
	}
	expr, ok := set["issue.launchfailures"].(bson.M)
	if !ok {
		t.Fatalf("issue.launchfailures = %#v, want an expression document", set["issue.launchfailures"])
	}
	slice, ok := expr["$slice"].(bson.A)
	if !ok || len(slice) != 2 {
		t.Fatalf("$slice = %#v, want [array, -N]", expr["$slice"])
	}
	if n, ok := slice[1].(int); !ok || n != -native.MaxLaunchFailures {
		t.Fatalf("$slice limit = %#v, want %d", slice[1], -native.MaxLaunchFailures)
	}
}
