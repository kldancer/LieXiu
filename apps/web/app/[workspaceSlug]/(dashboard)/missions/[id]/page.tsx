"use client";

import { use } from "react";
import { MissionPage } from "@liexiu/views/orchestration";
import { ErrorBoundary } from "@liexiu/ui/components/common/error-boundary";

export default function MissionRoute({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return (
    <ErrorBoundary resetKeys={[id]}>
      <MissionPage missionId={id} />
    </ErrorBoundary>
  );
}
