package types

import "testing"

func TestToolTelemetry_Record_nilReceiver(t *testing.T) {
	var tels *ToolTelemetry
	tels.Record("calc", false)
}

func TestToolTelemetry_Record_success(t *testing.T) {
	var tools ToolTelemetry
	tools.Record("calculator", false)
	if tools.TotalCalls != 1 || tools.FailedCalls != 0 {
		t.Fatalf("calls: total=%d failed=%d", tools.TotalCalls, tools.FailedCalls)
	}
	if tools.Breakdown["calculator"] != 1 {
		t.Fatalf("breakdown: %#v", tools.Breakdown)
	}
	if len(tools.FailedBreakdown) != 0 {
		t.Fatalf("unexpected failed breakdown: %#v", tools.FailedBreakdown)
	}
}

func TestToolTelemetry_Record_failed(t *testing.T) {
	var tools ToolTelemetry
	tools.Record("weather", true)
	if tools.TotalCalls != 1 || tools.FailedCalls != 1 {
		t.Fatalf("calls: total=%d failed=%d", tools.TotalCalls, tools.FailedCalls)
	}
	if tools.Breakdown["weather"] != 1 || tools.FailedBreakdown["weather"] != 1 {
		t.Fatalf("breakdown=%#v failed=%#v", tools.Breakdown, tools.FailedBreakdown)
	}
}

func TestToolTelemetry_Record_multiple(t *testing.T) {
	var tools ToolTelemetry
	tools.Record("calculator", false)
	tools.Record("calculator", true)
	tools.Record("mcp_srv_search", false)

	if tools.TotalCalls != 3 || tools.FailedCalls != 1 {
		t.Fatalf("calls: total=%d failed=%d", tools.TotalCalls, tools.FailedCalls)
	}
	if tools.Breakdown["calculator"] != 2 || tools.Breakdown["mcp_srv_search"] != 1 {
		t.Fatalf("breakdown: %#v", tools.Breakdown)
	}
	if tools.FailedBreakdown["calculator"] != 1 {
		t.Fatalf("failed breakdown: %#v", tools.FailedBreakdown)
	}
}

func TestToolTelemetry_Record_emptyName(t *testing.T) {
	var tools ToolTelemetry
	tools.Record("", true)
	if tools.TotalCalls != 1 || tools.FailedCalls != 1 {
		t.Fatalf("calls: total=%d failed=%d", tools.TotalCalls, tools.FailedCalls)
	}
	if len(tools.Breakdown) != 0 || len(tools.FailedBreakdown) != 0 {
		t.Fatalf("expected no breakdown keys for empty name, got breakdown=%#v failed=%#v", tools.Breakdown, tools.FailedBreakdown)
	}
}
