import { LoginPage } from "@liexiu/views/auth";
import { DragStrip } from "@liexiu/views/platform";
import { LieXiuIcon } from "@liexiu/ui/components/common/liexiu-icon";

export function DesktopLoginPage() {
  return (
    <div className="flex h-screen flex-col">
      <DragStrip />
      <LoginPage
        logo={<LieXiuIcon bordered size="lg" />}
        localBootstrap
        onSuccess={() => {
          // Auth store update triggers AppContent re-render and the existing
          // tab coordinator resolves the canonical workspace session.
        }}
      />
    </div>
  );
}
