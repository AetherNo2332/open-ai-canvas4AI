package app

import "testing"

func TestStoryboardReadFieldSchema(t *testing.T) {
	schema := workflowTool(t, "canvas_read_storyboard")
	for _, sample := range []struct {
		args  string
		valid bool
	}{
		{`{"nodeId":"b","fields":["imageGenerationPrompt","videoMotionPrompt","shotSize","durationSeconds"],"missingAnyFields":["imageGenerationPrompt","videoMotionPrompt"]}`, true},
		{`{"nodeId":"b","rowIds":["one","two"],"fields":["motion"]}`, true},
		{`{"nodeId":"b","fields":[]}`, false},
		{`{"nodeId":"b","fields":["motion","motion"]}`, false},
		{`{"nodeId":"b","fields":["status"]}`, false},
		{`{"nodeId":"b","missingAnyFields":["imageNodeId"]}`, false},
		{`{"nodeId":"b","missingAnyFields":[]}`, false},
		{`{"nodeId":"b","missingAnyFields":["dialogue","dialogue"]}`, false},
	} {
		err := validateCloudAgentToolArguments(schema, sample.args)
		if (err == nil) != sample.valid {
			t.Errorf("valid=%v error=%v args=%s", sample.valid, err, sample.args)
		}
	}
}

func TestCloudAgentSchemaUniqueItems(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"values": map[string]any{"type": "array", "uniqueItems": true}}}
	for _, sample := range []struct {
		args  string
		valid bool
	}{
		{`{"values":["a","b"]}`, true},
		{`{"values":[1,"1",true,"true",null,"null"]}`, true},
		{`{"values":["a","a"]}`, false},
		{`{"values":[1,1.0]}`, false},
		{`{"values":[{"a":1,"b":2},{"b":2,"a":1}]}`, false},
		{`{"values":[[1,"a"],[1,"a"]]}`, false},
	} {
		err := validateCloudAgentToolArguments(schema, sample.args)
		if (err == nil) != sample.valid {
			t.Errorf("valid=%v error=%v args=%s", sample.valid, err, sample.args)
		}
	}
}
