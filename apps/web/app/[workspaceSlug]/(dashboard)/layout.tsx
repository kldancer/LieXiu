"use client";

import { DashboardLayout } from "@liexiu/views/layout";
import { LieXiuIcon } from "@liexiu/ui/components/common/liexiu-icon";
import { SearchCommand, SearchTrigger } from "@liexiu/views/search";

export default function Layout({ children }: { children: React.ReactNode }) {
  return (
    <DashboardLayout
      loadingIndicator={<LieXiuIcon className="size-6" />}
      searchSlot={<SearchTrigger />}
      extra={
        <>
          <SearchCommand />
        </>
      }
    >
      {children}
    </DashboardLayout>
  );
}
