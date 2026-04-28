"use client";

import { CopilotKit } from "@copilotkit/react-core";
import { CopilotChat } from "@copilotkit/react-ui";
import "@copilotkit/react-ui/styles.css";

// CopilotKit talks to the Next.js runtime route (/api/copilotkit), which bridges to the Go agent.
export default function Home() {
  return (
    <CopilotKit runtimeUrl="/api/copilotkit" agent="default">
      <div style={{ height: "100vh", backgroundColor: "rgb(17, 17, 17)" }}>
        <CopilotChat />
      </div>
    </CopilotKit>
  );
}
