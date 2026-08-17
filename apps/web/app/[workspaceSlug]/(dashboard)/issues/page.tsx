"use client";

import { IssuesPage } from "@liexiu/views/issues/components";
import { ErrorBoundary } from "@liexiu/ui/components/common/error-boundary";

export default function Page() {
  return (
    <ErrorBoundary>
      <IssuesPage />
    </ErrorBoundary>
  );
}
