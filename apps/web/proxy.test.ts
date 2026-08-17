import { describe, expect, it } from "vitest";
import { NextRequest } from "next/server";
import { LIEXIU_LOCALE_HEADER } from "./lib/locale-routing";
import { proxy } from "./proxy";

function makeRequest(
  path: string,
  cookies: Record<string, string> = {},
  host = "app.liexiu.test",
) {
  const cookieHeader = Object.entries(cookies)
    .map(([key, value]) => `${key}=${value}`)
    .join("; ");

  return new NextRequest(`https://${host}${path}`, {
    headers: cookieHeader ? { cookie: cookieHeader } : undefined,
  });
}

function redirectLocation(
  path: string,
  cookies: Record<string, string> = {},
  host?: string,
) {
  return proxy(makeRequest(path, cookies, host)).headers.get("location");
}

function restoreEnv(key: string, value: string | undefined) {
  if (value === undefined) delete process.env[key];
  else process.env[key] = value;
}

function withoutRuntimeUpstreams(run: () => void) {
  const previousRemoteApiUrl = process.env.REMOTE_API_URL;
  const previousPublicApiUrl = process.env.NEXT_PUBLIC_API_URL;
  const previousPort = process.env.PORT;
  delete process.env.REMOTE_API_URL;
  delete process.env.NEXT_PUBLIC_API_URL;
  process.env.PORT = "3000";

  try {
    run();
  } finally {
    restoreEnv("REMOTE_API_URL", previousRemoteApiUrl);
    restoreEnv("NEXT_PUBLIC_API_URL", previousPublicApiUrl);
    restoreEnv("PORT", previousPort);
  }
}

describe("proxy legacy workspace route redirects", () => {
  const sessionCookies = {
    liexiu_logged_in: "1",
    last_workspace_slug: "acme",
  };

  it.each([
    ["issues", "/acme/issues"],
    ["projects", "/acme/projects"],
    ["agents", "/acme/agents"],
    ["my-issues", "/acme/my-issues"],
    ["runtimes", "/acme/runtimes"],
    ["skills", "/acme/skills"],
    ["settings", "/acme/settings"],
    ["usage", "/acme/usage"],
  ])(
    "redirects legacy /%s URLs through the last workspace slug",
    (segment, expectedPath) => {
      expect(redirectLocation(`/${segment}?tab=all`, sessionCookies)).toBe(
        `https://app.liexiu.test${expectedPath}?tab=all`,
      );
    },
  );

  it("preserves nested legacy paths and query strings", () => {
    expect(
      redirectLocation("/projects/project-123?view=issues", sessionCookies),
    ).toBe("https://app.liexiu.test/acme/projects/project-123?view=issues");
  });

  it("sends logged-out legacy URLs to login", () => {
    expect(redirectLocation("/usage?tab=runtime")).toBe(
      "https://app.liexiu.test/login?tab=runtime",
    );
  });

  it("sends logged-in legacy URLs without a last workspace cookie to root", () => {
    expect(
      redirectLocation("/projects", { liexiu_logged_in: "1" }),
    ).toBe("https://app.liexiu.test/");
  });

  it("does not redirect workspace-scoped URLs whose first segment is already a slug", () => {
    expect(redirectLocation("/acme/projects", sessionCookies)).toBeNull();
  });

  it("redirects app-host root URLs to the last workspace", () => {
    expect(redirectLocation("/", sessionCookies)).toBe(
      "https://app.liexiu.test/acme/issues",
    );
  });

  it("redirects root URLs on every host to the last workspace", () => {
    expect(redirectLocation("/", sessionCookies, "liexiu.ai")).toBe(
      "https://liexiu.ai/acme/issues",
    );
  });
});

describe("proxy runtime upstream rewrites", () => {
  it("does not rewrite API requests when no runtime API origin is configured", () => {
    withoutRuntimeUpstreams(() => {
      const res = proxy(makeRequest("/api/config?x=1"));

      expect(res.status).toBe(200);
      expect(res.headers.get("x-middleware-rewrite")).toBeNull();
      expect(
        res.headers.get(`x-middleware-request-${LIEXIU_LOCALE_HEADER}`),
      ).toBe("en");
    });
  });

  it("rewrites API requests to the runtime API origin", () => {
    const previous = process.env.REMOTE_API_URL;
    process.env.REMOTE_API_URL = "http://backend:8080";
    try {
      const res = proxy(makeRequest("/api/config?x=1"));

      expect(res.status).toBe(200);
      expect(res.headers.get("x-middleware-rewrite")).toBe(
        "http://backend:8080/api/config?x=1",
      );
    } finally {
      restoreEnv("REMOTE_API_URL", previous);
    }
  });

  it("rewrites websocket requests to the runtime API origin", () => {
    const previous = process.env.REMOTE_API_URL;
    process.env.REMOTE_API_URL = "http://backend:8080";
    try {
      const res = proxy(makeRequest("/ws"));

      expect(res.status).toBe(200);
      expect(res.headers.get("x-middleware-rewrite")).toBe(
        "http://backend:8080/ws",
      );
    } finally {
      restoreEnv("REMOTE_API_URL", previous);
    }
  });

  it("does not rewrite removed public auth pages", () => {
    const previous = process.env.REMOTE_API_URL;
    process.env.REMOTE_API_URL = "http://backend:8080";
    try {
      const res = proxy(makeRequest("/auth/callback"));

      expect(res.status).toBe(200);
      expect(res.headers.get("x-middleware-rewrite")).toBeNull();
      expect(
        res.headers.get(`x-middleware-request-${LIEXIU_LOCALE_HEADER}`),
      ).toBe("en");
    } finally {
      restoreEnv("REMOTE_API_URL", previous);
    }
  });
});

describe("proxy root and locale handling", () => {
  it("redirects logged-in root visits to the last workspace", () => {
    const res = proxy(
      makeRequest("/", {
        liexiu_logged_in: "1",
        last_workspace_slug: "acme",
      }),
    );

    expect(res.status).toBe(307);
    expect(res.headers.get("location")).toBe(
      "https://app.liexiu.test/acme/issues",
    );
  });

  it("forwards locale on login requests", () => {
    const res = proxy(makeRequest("/login", { "liexiu-locale": "zh-Hans" }));

    expect(res.status).toBe(200);
    expect(res.headers.get("location")).toBeNull();
    expect(
      res.headers.get(`x-middleware-request-${LIEXIU_LOCALE_HEADER}`),
    ).toBe("zh-Hans");
  });
});
