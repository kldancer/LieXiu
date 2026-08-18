"use client";

import { useCallback, useEffect, useState } from "react";
import { useNavigation } from "../navigation";

export const MISSION_VIEW_MODES = ["world", "replay"] as const;
export type MissionViewMode = (typeof MISSION_VIEW_MODES)[number];

export function useMissionViewMode() {
  const navigation = useNavigation();
  const urlMode = normalizeMode(navigation.searchParams.get("view"));
  const [mode, setModeState] = useState<MissionViewMode>(urlMode);

  useEffect(() => setModeState(urlMode), [urlMode]);

  const setMode = useCallback((nextMode: MissionViewMode) => {
    setModeState(nextMode);
    const params = new URLSearchParams(navigation.searchParams);
    params.set("view", nextMode);
    const query = params.toString();
    navigation.replace(`${navigation.pathname}${query ? `?${query}` : ""}`);
  }, [navigation]);

  return { mode, setMode };
}

function normalizeMode(value: string | null): MissionViewMode {
  return MISSION_VIEW_MODES.includes(value as MissionViewMode) ? value as MissionViewMode : "world";
}
