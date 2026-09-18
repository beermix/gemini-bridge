package proxy

import (
	"testing"
)

func TestThinkingFenceStripper_StreamAndBatch(t *testing.T) {
	// 1. Standalone ```thinking opener should be stripped
	input := "preamble\n```thinking\nactual thoughts\n```"
	stripped := StripThinkingFenceDelimiters(input)
	expected := "preamble\nactual thoughts\n```"
	if stripped != expected {
		t.Fatalf("expected %q, got %q", expected, stripped)
	}

	// 2. Extra backticks: ``````thinking should be stripped
	input2 := "``````thinking\n*Thinking deeply*\n"
	stripped2 := StripThinkingFenceDelimiters(input2)
	expected2 := "*Thinking deeply*\n"
	if stripped2 != expected2 {
		t.Fatalf("expected %q, got %q", expected2, stripped2)
	}

	// 3. Regular code block (e.g. ```go or ```rs) must NOT be stripped
	inputCode := "Here is code:\n```go\nfunc main() {}\n```\nDone."
	strippedCode := StripThinkingFenceDelimiters(inputCode)
	if strippedCode != inputCode {
		t.Fatalf("expected code block to be preserved, got %q", strippedCode)
	}

	// 4. Streaming chunks splitting the fence
	stripper := &ThinkingFenceStripper{}
	c1 := stripper.Push("step 1\n``")
	c2 := stripper.Push("`thinking\nstep 2\n")
	c3 := stripper.Flush()
	streamResult := c1 + c2 + c3
	expectedStream := "step 1\nstep 2\n"
	if streamResult != expectedStream {
		t.Fatalf("expected streaming strip %q, got %q", expectedStream, streamResult)
	}
}

func TestPlanningBuffer_Interception(t *testing.T) {
	// 1. Plain message without leak
	pb := &PlanningBuffer{}
	out1, leak1 := pb.Consume("Hello world!", true)
	if leak1 || out1 != "Hello world!" {
		t.Fatalf("plain text should not be treated as leak: out=%q, leak=%v", out1, leak1)
	}

	// 2. Chunked planning leak stripped, visible suffix emitted
	pb2 := &PlanningBuffer{}
	o1, l1 := pb2.Consume(`{"thought":`, false)
	if o1 != "" || l1 {
		t.Fatalf("expected empty buffer while incomplete: out=%q, leak=%v", o1, l1)
	}
	o2, l2 := pb2.Consume(` "analyzing problem"}Final answer here.`, true)
	if !l2 || o2 != "Final answer here." {
		t.Fatalf("expected planning leak stripped and rest emitted: out=%q, leak=%v", o2, l2)
	}
	if !pb2.StrippedPlanning() {
		t.Fatalf("expected StrippedPlanning to be true")
	}

	// 3. Tool signature JSON stripped
	pb3 := &PlanningBuffer{}
	o3, l3 := pb3.Consume(`{"call": "run_command", "command": "ls"}Result`, true)
	if !l3 || o3 != "Result" {
		t.Fatalf("expected call leak stripped: out=%q, leak=%v", o3, l3)
	}
}
