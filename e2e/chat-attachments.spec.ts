import "./env";
import { expect, test } from "@playwright/test";
import pg from "pg";
import { createTestApi } from "./helpers";

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL || `http://localhost:${process.env.PORT || "8080"}`;
const DATABASE_URL =
  process.env.DATABASE_URL ??
  "postgres://liexiu:liexiu@localhost:5432/liexiu?sslmode=disable";

test("personal v1 retires chat-session attachment state and routes", async () => {
  const api = await createTestApi();
  const token = api.getToken();
  if (!token) throw new Error("test api client not logged in");

  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    const result = await client.query<{
      chat_session: string | null;
      chat_message: string | null;
      attachment_chat_session: string | null;
      attachment_chat_message: string | null;
    }>(`
      SELECT
        to_regclass('public.chat_session')::text AS chat_session,
        to_regclass('public.chat_message')::text AS chat_message,
        (SELECT column_name FROM information_schema.columns
          WHERE table_schema='public' AND table_name='attachment'
            AND column_name='chat_session_id') AS attachment_chat_session,
        (SELECT column_name FROM information_schema.columns
          WHERE table_schema='public' AND table_name='attachment'
            AND column_name='chat_message_id') AS attachment_chat_message
    `);
    expect(result.rows[0]).toEqual({
      chat_session: null,
      chat_message: null,
      attachment_chat_session: null,
      attachment_chat_message: null,
    });
  } finally {
    await client.end();
  }

  const response = await fetch(
    `${API_BASE}/api/chat/sessions/00000000-0000-0000-0000-000000000000/messages`,
    { headers: { Authorization: `Bearer ${token}` } },
  );
  expect(response.status).toBe(404);
});
