"use client";

import { useCallback, useEffect, useState } from "react";
import { useNavigation } from "../navigation";

const RUN_QUERY_KEY = "run";

/** Keeps Board, World and Inspector on the same URL-addressable Run ID. */
export function useMissionRunSelection(availableRunIds: ReadonlySet<string>) {
  const navigation = useNavigation();
  const urlRunId = navigation.searchParams.get(RUN_QUERY_KEY) ?? "";
  const [preferredRunId, setPreferredRunId] = useState(urlRunId);

  useEffect(() => {
    setPreferredRunId(urlRunId);
  }, [urlRunId]);

  const firstRunId = availableRunIds.values().next().value ?? "";
  const selectedRunId = availableRunIds.has(preferredRunId)
    ? preferredRunId
    : firstRunId;

  const selectRun = useCallback((runId: string) => {
    const nextRunId = availableRunIds.has(runId) ? runId : "";
    setPreferredRunId(nextRunId);
    const params = new URLSearchParams(navigation.searchParams);
    if (nextRunId) params.set(RUN_QUERY_KEY, nextRunId);
    else params.delete(RUN_QUERY_KEY);
    const query = params.toString();
    navigation.replace(`${navigation.pathname}${query ? `?${query}` : ""}`);
  }, [availableRunIds, navigation]);

  return { selectedRunId, selectRun };
}
