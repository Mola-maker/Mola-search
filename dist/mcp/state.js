import { ALL_TOOL_NAMES, BUILTIN_PROFILES, normalizeProfile } from "../config.js";
import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { join } from "node:path";
export { normalizeProfile } from "../config.js";
const RUNTIME_FILE = "runtime.json";
export function loadRuntime(config) {
    const enabledTools = new Set(config.enabledTools);
    const customProfiles = new Map();
    const p = join(config.dataDir, RUNTIME_FILE);
    if (existsSync(p)) {
        try {
            const raw = JSON.parse(readFileSync(p, "utf8"));
            if (Array.isArray(raw.enabledTools)) {
                enabledTools.clear();
                for (const tool of raw.enabledTools) {
                    if (ALL_TOOL_NAMES.includes(tool))
                        enabledTools.add(tool);
                }
            }
            if (Array.isArray(raw.customProfiles)) {
                for (const profile of raw.customProfiles) {
                    if (profile && typeof profile.name === "string")
                        customProfiles.set(profile.name, normalizeProfile(profile));
                }
            }
        }
        catch {
            /* 损坏则忽略，走默认 */
        }
    }
    return { enabledTools, customProfiles };
}
export function persistRuntime(config, ctx) {
    try {
        writeFileSync(join(config.dataDir, RUNTIME_FILE), JSON.stringify({ enabledTools: [...ctx.enabledTools], customProfiles: [...ctx.customProfiles.values()] }, null, 2), "utf8");
    }
    catch {
        /* ignore */
    }
}
export function configuredCustomProfiles(ctx) {
    const profiles = new Map();
    for (const profile of ctx.config.profiles) {
        if (!BUILTIN_PROFILES.includes(profile))
            profiles.set(profile.name, profile);
    }
    for (const [name, profile] of ctx.customProfiles)
        profiles.set(name, profile);
    return [...profiles.values()];
}
