import { describe, expect, it } from "vitest";
import type { ActivityProjection, TaskNodeProjection, TeamMemberProjection } from "@liexiu/core/orchestration";
import {
  boardLaneForStatus,
  buildDagLayout,
  buildMailboxActivityViews,
  buildPixelActors,
  pixelActionForActivity,
  pixelActorState,
  worldZoneForStatus,
} from "./view-model";

describe("orchestration visual mappings", () => {
  it("keeps business review distinct from execution", () => {
    expect(boardLaneForStatus("running")).toBe("active");
    expect(boardLaneForStatus("review")).toBe("review");
    expect(worldZoneForStatus("review")).toBe("reviewLab");
    expect(pixelActorState("review", "running")).toBe("reviewing");
  });

  it("maps terminal and unknown states to safe visible zones", () => {
    expect(boardLaneForStatus("failed")).toBe("attention");
    expect(worldZoneForStatus("cancelled")).toBe("blocked");
    expect(pixelActorState(undefined, "completed")).toBe("done");
  });

  it("builds stable DAG columns and keeps malformed cycles visible", () => {
    const plan = node("node-plan", "PLAN", []);
    const execute = node("node-execute", "EXECUTE", [plan.id]);
    const review = node("node-review", "REVIEW", [execute.id]);
    const columns = buildDagLayout([review, plan, execute]);

    expect(columns.map((column) => column.map((item) => item.key))).toEqual([
      ["PLAN"],
      ["EXECUTE"],
      ["REVIEW"],
    ]);
    expect(columns[2]?.[0]?.dependencyKeys).toEqual(["EXECUTE"]);

    const cycleA = node("cycle-a", "A", ["cycle-b"]);
    const cycleB = node("cycle-b", "B", ["cycle-a"]);
    expect(buildDagLayout([cycleB, cycleA])[0]?.map((item) => item.key)).toEqual(["A", "B"]);
  });

  it("places actors deterministically regardless of response ordering", () => {
    const running = { ...node("node-run", "RUN", []), status: "running" as const };
    const agents: TeamMemberProjection[] = [agent("agent-b", "Reviewer", "reviewer"), agent("agent-a", "Executor", "executor")];
    agents[0]!.currentNodeIds = [running.id];
    agents[1]!.currentNodeIds = [running.id];

    const first = buildPixelActors(agents, [running], "running");
    const second = buildPixelActors([...agents].reverse(), [running], "running");
    expect(first.map(({ agent: item, zone, slot, paletteIndex }) => [item.agentId, zone, slot, paletteIndex]))
      .toEqual(second.map(({ agent: item, zone, slot, paletteIndex }) => [item.agentId, zone, slot, paletteIndex]));
    expect(first.every((item) => item.state === "working" && item.zone === "workshop")).toBe(true);
  });

  it("derives transient animation cues from Activity without changing business state", () => {
    expect(pixelActionForActivity("run.started")).toBe("move");
    expect(pixelActionForActivity("review.approved")).toBe("celebrate");
    expect(pixelActionForActivity("task.blocked")).toBe("alert");
    expect(pixelActionForActivity(undefined)).toBe("none");
  });

	it("builds safe deterministic mailbox summaries and drops duplicate or malformed events", () => {
		const expired = mailboxActivity("event-expired", 12, "mailbox.message_expired", "expired", 8);
		const pending = mailboxActivity("event-pending", 11, "mailbox.message_sent", "pending", 0);
		const duplicate = { ...pending, id: "event-pending-replay" };
		const malformed = { ...mailboxActivity("event-malformed", 13, "mailbox.message_sent", "pending", 0), payload: { secret: "must-not-render" } };
		const views = buildMailboxActivityViews([expired, malformed, duplicate, pending]);

		expect(views.map(({ sequence, status, hops }) => [sequence, status, hops])).toEqual([
			[11, "pending", 0],
			[12, "expired", 8],
		]);
		expect(views[0]).not.toHaveProperty("payload");
	});
});

function mailboxActivity(id: string, sequence: number, type: string, status: string, hops: number): ActivityProjection {
	return {
		id, sequence, type, taskNodeId: "node-run", runId: "run-1", actorType: "agent", actorId: "agent-a",
		subjectType: "mailbox_message", subjectId: "message-1", causationId: "command-1", correlationId: "command-1",
		payloadVersion: 1, occurredAt: "2026-08-19T00:00:00Z",
		payload: {
			message_id: "message-1", message_type: "context_request", recipient_type: "agent", recipient_id: "agent-b",
			from_status: status === "pending" ? "" : "pending", to_status: status,
			expires_at: "2026-08-19T01:00:00Z", hops,
		},
	};
}

function node(id: string, key: string, dependencyIds: string[]): TaskNodeProjection {
  return {
    id,
    key,
    title: key,
    description: "",
    duty: "executor",
    status: "pending",
    dependencyIds,
    acceptanceCriteria: [],
    artifactKinds: [],
    reworkCount: 0,
    revision: 1,
  };
}

function agent(agentId: string, agentName: string, duty: TeamMemberProjection["duty"]): TeamMemberProjection {
  return {
    agentId,
    agentName,
    duty,
    runtimeId: `runtime-${agentId}`,
    runtimeName: "runtime",
    runtimeStatus: "online",
    provider: "test",
    capabilities: {},
    currentNodeIds: [],
  };
}
