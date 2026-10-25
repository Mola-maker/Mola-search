import type { PluginConfig, SwarmProfile } from "../config.js";
import { ALL_TOOL_NAMES, BUILTIN_PROFILES, normalizeProfile } from "../config.js";
import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { join } from "node:path";
import type { WsaClient } from "../core/wsa-client.js";
import type { SwarmEngine } from "../swarm/engine.js";
import type { HistoryStore } from "../util/text.js";

export { normalizeProfile } from "../config.js";

export interface RunContext {
  config: PluginConfig;
  client: WsaClient;
  engine: SwarmEngine;
  history: HistoryStore;
  /** 动态启用的工具集合（运行时可变，跨重启持久化） */
  enabledTools: Set<string>;
  /** 运行时注册的自定义 Profiles */
  customProfiles: Map<string, SwarmProfile>;
  persist: () => void;
  /** 同步工具注册表；仅工具集合变化时通知一次 */
  reconfigure: () => void;
  /** 重新读取项目配置、.env 与持久化运行状态 */
  reload: () => void;
}

const RUNTIME_FILE = "runtime.json";

export function loadRuntime(config: PluginConfig): Pick<RunContext, "enabledTools" | "customProfiles"> {
  const enabledTools = new Set<string>(config.enabledTools);
  const customProfiles = new Map<string, SwarmProfile>();
  const p = join(config.dataDir, RUNTIME_FILE);
  if (existsSync(p)) {
    try {
      const raw = JSON.parse(readFileSync(p, "utf8")) as {
        enabledTools?: string[];
        customProfiles?: SwarmProfile[];
      };
      if (Array.isArray(raw.enabledTools)) {
        enabledTools.clear();
        for (const tool of raw.enabledTools) {
          if ((ALL_TOOL_NAMES as readonly string[]).includes(tool)) enabledTools.add(tool);
        }
      }
      if (Array.isArray(raw.customProfiles)) {
        for (const profile of raw.customProfiles) {
          if (profile && typeof profile.name === "string") customProfiles.set(profile.name, normalizeProfile(profile));
        }
      }
    } catch {
      /* 损坏则忽略，走默认 */
    }
  }
  return { enabledTools, customProfiles };
}

export function persistRuntime(config: PluginConfig, ctx: Pick<RunContext, "enabledTools" | "customProfiles">): void {
  try {
    writeFileSync(
      join(config.dataDir, RUNTIME_FILE),
      JSON.stringify(
        { enabledTools: [...ctx.enabledTools], customProfiles: [...ctx.customProfiles.values()] },
        null,
        2,
      ),
      "utf8",
    );
  } catch {
    /* ignore */
  }
}

export function configuredCustomProfiles(ctx: Pick<RunContext, "config" | "customProfiles">): SwarmProfile[] {
  const profiles = new Map<string, SwarmProfile>();
  for (const profile of ctx.config.profiles) {
    if (!BUILTIN_PROFILES.includes(profile)) profiles.set(profile.name, profile);
  }
  for (const [name, profile] of ctx.customProfiles) profiles.set(name, profile);
  return [...profiles.values()];
}
