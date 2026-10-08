package repository

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func TestLinkAnalyticsPipelineFiltersAndFacets(t *testing.T) {
	pipeline := linkAnalyticsPipeline("my-link", "2026-10-01", "2026-10-08")
	if len(pipeline) != 2 {
		t.Fatalf("pipeline stages = %d, want 2", len(pipeline))
	}

	match, ok := pipeline[0][0].Value.(bson.M)
	if !ok {
		t.Fatalf("match stage = %#v", pipeline[0][0].Value)
	}
	if match["code"] != "my-link" {
		t.Fatalf("match code = %v, want my-link", match["code"])
	}
	wantDates := bson.M{"$gte": "2026-10-01", "$lte": "2026-10-08"}
	if !reflect.DeepEqual(match["date"], wantDates) {
		t.Fatalf("match date = %#v, want %#v", match["date"], wantDates)
	}

	facets, ok := pipeline[1][0].Value.(bson.M)
	if !ok {
		t.Fatalf("facet stage = %#v", pipeline[1][0].Value)
	}
	for _, name := range []string{"daily", "total", "referrers", "browsers"} {
		if _, exists := facets[name]; !exists {
			t.Fatalf("facet %q is missing", name)
		}
	}
}

func TestStatisticPipelineAggregatesDynamicCounters(t *testing.T) {
	pipeline := statisticPipeline("referrers")
	if len(pipeline) != 5 {
		t.Fatalf("pipeline stages = %d, want 5", len(pipeline))
	}
	project, ok := pipeline[0][0].Value.(bson.M)
	if !ok {
		t.Fatalf("project stage = %#v", pipeline[0][0].Value)
	}
	entries, ok := project["entries"].(bson.M)
	if !ok {
		t.Fatalf("entries expression = %#v", project["entries"])
	}
	if _, exists := entries["$objectToArray"]; !exists {
		t.Fatalf("entries expression = %#v, want $objectToArray", entries)
	}
}
