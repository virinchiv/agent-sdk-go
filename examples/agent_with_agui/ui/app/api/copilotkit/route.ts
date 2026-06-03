import {
  CopilotRuntime,
  ExperimentalEmptyAdapter,
  copilotRuntimeNextJSAppRouterEndpoint,
} from "@copilotkit/runtime";
import { HttpAgent } from "@ag-ui/client";
import { NextRequest } from "next/server";

const agentURL = process.env.AGENT_URL ?? "http://127.0.0.1:8787/agui";

const runtime = new CopilotRuntime({
  agents: {
    // CopilotKit runtime typings lag @ag-ui/client; HttpAgent is the supported bridge.
    default: new HttpAgent({ url: agentURL }) as any,
  },
});

export async function POST(req: NextRequest) {
  const serviceAdapter = new ExperimentalEmptyAdapter();
  const { handleRequest } = copilotRuntimeNextJSAppRouterEndpoint({
    runtime,
    serviceAdapter,
    endpoint: "/api/copilotkit",
  });
  return handleRequest(req);
}
