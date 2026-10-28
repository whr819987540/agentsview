import type { SessionGroup, SessionGroupInput } from "../../stores/sessions.svelte.js";
import { sessionAncestryMatches } from "../../utils/session-ancestry.js";

export const ITEM_HEIGHT = 42;
const CHILD_ITEM_HEIGHT = 34;
const TEAM_HEADER_HEIGHT = 28;
export const HEADER_HEIGHT = 28;
export const FOLDER_HEADER_HEIGHT = 28;
export const FOLDER_GAP_HEIGHT = 10;
export const OVERSCAN = 10;
export const STORAGE_KEY = "agentsview-group-by-agent";
export const STORAGE_KEY_GROUP = "agentsview-group-mode";

export type GroupMode = "none" | "agent" | "project";

export interface GroupSection {
  label: string;
  groups: SessionGroup[];
}

/** @deprecated Use GroupSection */
export type AgentSection = GroupSection;

/** A sidebar folder that groups one project's sessions. */
export interface SidebarFolder {
  id: number;
  project: string;
  name: string;
}

/**
 * Where folders apply. In "project" mode every project section shows its
 * folders; otherwise only a list filtered to one project does.
 */
export interface FolderView {
  /** The filtered project when the list shows a single project. */
  project: string | null;
  /** Folders of each project in display order. */
  foldersByProject: ReadonlyMap<string, readonly SidebarFolder[]>;
  /** Folder of each filed session group, keyed by group key. */
  groupFolder: ReadonlyMap<string, number>;
  /** Folders whose sessions are shown. */
  expanded: ReadonlySet<number>;
}

export interface DisplayItem {
  id: string;
  type: "header" | "folder" | "gap" | "session" | "team-group" | "subagent-group";
  label: string;
  count: number;
  group?: SessionGroup;
  /** The folder a "folder" row stands for. */
  folder?: SidebarFolder;
  /** Extra indent levels: 1 for rows inside a folder. */
  inset?: number;
  /** Drop target for dragged sessions: a folder ID, or null for the
   *  project's top level. Undefined where folders do not apply. */
  dropFolderId?: number | null;
  /** Project of the drop target, so a drop from another project is refused. */
  dropProject?: string;
  /** Session groups represented by a collapsible section header. */
  sectionGroups?: SessionGroup[];
  /** Sessions represented by a collapsible child-group header. */
  memberSessionIds?: string[];
  /** For child items within an expanded continuation chain. */
  session?: SessionGroupInput;
  /** True when this is a child session inside an expanded group. */
  isChild?: boolean;
  /** Nesting depth: 0 = root, 1 = child/team-group, 2 = teammate. */
  depth?: number;
  /** True when this is the last sibling at its depth level. */
  isLastChild?: boolean;
  /** True when this depth-1 item has a following depth-1 sibling
   *  (used to draw the vertical connector line through this row). */
  hasNextSiblingAtDepth1?: boolean;
  height: number;
  top: number;
}

/**
 * Read persisted group mode from localStorage, migrating the
 * legacy boolean key if needed.
 */
export function getInitialGroupMode(): GroupMode {
  if (typeof localStorage === "undefined") return "none";
  const stored = localStorage.getItem(STORAGE_KEY_GROUP);
  if (stored === "agent" || stored === "project") return stored;
  // Legacy migration
  if (localStorage.getItem(STORAGE_KEY) === "true") return "agent";
  return "none";
}

/**
 * Find the folder of each session group. A group follows the folder of its
 * primary session, else of the first filed member. Folders of another
 * project are ignored, so a session moved to a new project leaves its old
 * project's folder.
 */
export function assignGroupFolders(
  groups: SessionGroup[],
  folderIdBySession: ReadonlyMap<string, number>,
  folderProjects: ReadonlyMap<number, string>,
): Map<string, number> {
  const out = new Map<string, number>();
  if (folderIdBySession.size === 0) return out;
  for (const g of groups) {
    const members = g.allSessions ?? g.sessions;
    const primary = members.find((s) => s.id === g.primarySessionId) ?? g.sessions[0];
    if (!primary) continue;
    const candidates = [primary, ...members.filter((s) => s !== primary)];
    for (const s of candidates) {
      const folderId = folderIdBySession.get(s.id);
      if (folderId !== undefined && folderProjects.get(folderId) === primary.project) {
        out.set(g.key, folderId);
        break;
      }
    }
  }
  return out;
}

/** Same head and status rules as buildSessionGroups. */
export { selectLiveId, selectPrimaryId } from "../../utils/session-primary.js";

/**
 * Build grouped sections from flat session groups.
 * Groups by agent name or project depending on mode.
 * Returns empty array when mode is "none".
 */
export function buildGroupSections(groups: SessionGroup[], mode: GroupMode): GroupSection[] {
  if (mode === "none") return [];
  const map = new Map<string, SessionGroup[]>();
  for (const g of groups) {
    const primary = g.sessions.find((s) => s.id === g.primarySessionId) ?? g.sessions[0];
    if (!primary) continue;
    const key = mode === "agent" ? primary.agent : primary.project;
    let list = map.get(key);
    if (!list) {
      list = [];
      map.set(key, list);
    }
    list.push(g);
  }
  // Sort by count descending (most sessions first).
  return Array.from(map.entries())
    .sort((a, b) => b[1].length - a[1].length)
    .map(([label, groups]) => ({ label, groups }));
}

/** Check if a session is a teammate (received a <teammate-message>). */
function isTeammateByMessage(s: SessionGroupInput): boolean {
  return s.is_teammate ?? s.first_message?.includes("<teammate-message") ?? false;
}

/**
 * Check if a session is a teammate, inheriting status from the
 * parent.  Continuation sessions of a teammate don't carry the
 * `<teammate-message>` tag themselves, but they belong to the
 * same teammate chain.
 */
function isTeammate(s: SessionGroupInput, allSessions: SessionGroupInput[]): boolean {
  return sessionAncestryMatches(s, allSessions, isTeammateByMessage);
}

/**
 * Check if a session is a subagent (has relationship_type === "subagent").
 * Continuation/fork sessions are NOT subagents.
 */
function isSubagent(s: SessionGroupInput): boolean {
  return s.relationship_type === "subagent";
}

/**
 * Check if a session is a subagent descendant — either it has
 * relationship_type === "subagent" itself, or one of its ancestors
 * in the group does.  This ensures that continuations/forks of a
 * subagent stay under the "Subagents" group header instead of
 * being miscategorised as plain continuations.
 */
export function isSubagentDescendant(
  s: SessionGroupInput,
  groupSessions: SessionGroupInput[],
): boolean {
  return sessionAncestryMatches(s, groupSessions, isSubagent);
}

interface GroupChildren {
  subagents: SessionGroupInput[];
  teammates: SessionGroupInput[];
  continuations: SessionGroupInput[];
}

/**
 * Separate a group's children into subagents, teammates, and
 * continuations. Use allSessions (unfiltered) for ancestry
 * classification so that filtered-out ancestors don't break the
 * parent-chain walk.
 */
function classifyChildren(g: SessionGroup): GroupChildren {
  const ancestryPool = g.allSessions ?? g.sessions;
  const out: GroupChildren = { subagents: [], teammates: [], continuations: [] };
  for (const s of g.sessions) {
    if (s.id === g.primarySessionId) continue;
    if (isSubagentDescendant(s, ancestryPool)) {
      out.subagents.push(s);
    } else if (isTeammate(s, ancestryPool)) {
      out.teammates.push(s);
    } else {
      out.continuations.push(s);
    }
  }
  return out;
}

/**
 * The expandedGroups keys that "Expand all" adds for a group: the
 * group itself and its Team sub-group. Subagents are left out, so a
 * group whose only children are subagents stays closed and the
 * Subagents sub-group keeps its state.
 */
export function bulkExpandKeys(g: SessionGroup): string[] {
  const { teammates, continuations } = classifyChildren(g);
  if (teammates.length > 0) return [g.key, `team:${g.key}`];
  return continuations.length > 0 ? [g.key] : [];
}

/**
 * Emit display items for a single SessionGroup, expanding
 * child sessions when the group key is in expandedGroups.
 *
 * When a group contains teammate sessions, they are placed
 * under a synthetic "Team (N)" expandable node at depth 1,
 * and the teammates themselves render at depth 2. Regular
 * subagents remain at depth 1. This gives the 3-level tree:
 *   Session (depth 0) > Subagent (depth 1)
 *   Session (depth 0) > Team (depth 1) > Teammate (depth 2)
 */
function emitGroupItems(
  g: SessionGroup,
  label: string,
  expandedGroups: Set<string>,
  items: DisplayItem[],
  y: { value: number },
): void {
  const hasChildren = g.sessions.length > 1;
  const isExpanded = hasChildren && expandedGroups.has(g.key);

  // Primary session (depth 0)
  items.push({
    id: label ? `session:${label}:${g.primarySessionId}` : `session:${g.primarySessionId}`,
    type: "session",
    label,
    count: 0,
    group: g,
    depth: 0,
    height: ITEM_HEIGHT,
    top: y.value,
  });
  y.value += ITEM_HEIGHT;

  if (!isExpanded) return;

  const { subagents, teammates, continuations } = classifyChildren(g);

  // Sort each child group most-recent-first so the sidebar
  // matches the main session list ordering convention.
  const byRecencyDesc = (a: SessionGroupInput, b: SessionGroupInput) => {
    const ka = a.ended_at ?? a.started_at ?? a.created_at;
    const kb = b.ended_at ?? b.started_at ?? b.created_at;
    return ka > kb ? -1 : ka < kb ? 1 : 0;
  };
  continuations.sort(byRecencyDesc);
  subagents.sort(byRecencyDesc);
  teammates.sort(byRecencyDesc);

  // Continuations render inline at depth 1 (no sub-group header).
  for (let i = 0; i < continuations.length; i++) {
    const s = continuations[i]!;
    // Determine if this is the last depth-1 sibling (accounting
    // for subsequent sub-group headers).
    const hasFollowingGroup = subagents.length > 0 || teammates.length > 0;
    const isLast = i === continuations.length - 1 && !hasFollowingGroup;
    items.push({
      id: `child:${g.key}:${s.id}`,
      type: "session",
      label,
      count: 0,
      group: g,
      session: s,
      isChild: true,
      depth: 1,
      isLastChild: isLast,
      height: CHILD_ITEM_HEIGHT,
      top: y.value,
    });
    y.value += CHILD_ITEM_HEIGHT;
  }

  // Count depth-1 group headers (subagents + team).
  const hasSubagentGroup = subagents.length > 0;
  const hasTeamGroup = teammates.length > 0;
  const depth1Count = (hasSubagentGroup ? 1 : 0) + (hasTeamGroup ? 1 : 0);
  let depth1Idx = 0;

  // Emit "Subagents (N)" group header + children at depth 2.
  if (hasSubagentGroup) {
    const subKey = `subagent:${g.key}`;
    const subExpanded = expandedGroups.has(subKey);

    items.push({
      id: `subagent-group:${g.key}`,
      type: "subagent-group",
      label: "Subagents",
      count: subagents.length,
      group: g,
      memberSessionIds: subagents.map((session) => session.id),
      depth: 1,
      isLastChild: depth1Idx === depth1Count - 1,
      height: TEAM_HEADER_HEIGHT,
      top: y.value,
    });
    y.value += TEAM_HEADER_HEIGHT;
    depth1Idx++;

    if (subExpanded) {
      for (let i = 0; i < subagents.length; i++) {
        const s = subagents[i]!;
        items.push({
          id: `child:${g.key}:${s.id}`,
          type: "session",
          label,
          count: 0,
          group: g,
          session: s,
          isChild: true,
          depth: 2,
          isLastChild: i === subagents.length - 1,
          height: CHILD_ITEM_HEIGHT,
          top: y.value,
        });
        y.value += CHILD_ITEM_HEIGHT;
      }
    }
  }

  // Emit "Team (N)" group header + children at depth 2.
  if (hasTeamGroup) {
    const teamKey = `team:${g.key}`;
    const teamExpanded = expandedGroups.has(teamKey);

    items.push({
      id: `team-group:${g.key}`,
      type: "team-group",
      label: "Team",
      count: teammates.length,
      group: g,
      memberSessionIds: teammates.map((session) => session.id),
      depth: 1,
      isLastChild: depth1Idx === depth1Count - 1,
      height: TEAM_HEADER_HEIGHT,
      top: y.value,
    });
    y.value += TEAM_HEADER_HEIGHT;

    if (teamExpanded) {
      for (let i = 0; i < teammates.length; i++) {
        const s = teammates[i]!;
        items.push({
          id: `child:${g.key}:${s.id}`,
          type: "session",
          label,
          count: 0,
          group: g,
          session: s,
          isChild: true,
          depth: 2,
          isLastChild: i === teammates.length - 1,
          height: CHILD_ITEM_HEIGHT,
          top: y.value,
        });
        y.value += CHILD_ITEM_HEIGHT;
      }
    }
  }
}

/**
 * Build a flat list of DisplayItems for virtual scrolling.
 * When mode is "none", produces a simple flat list.
 * Otherwise, interleaves header rows and session rows,
 * respecting collapsed groups. Continuation chains expand
 * inline when their group key is in expandedGroups. Where folders
 * apply, a project's folders come first, each followed by its
 * sessions when expanded, then the project's unfiled sessions. A gap
 * row separates an open last folder's sessions from the unfiled ones.
 */
export function buildDisplayItems(
  groups: SessionGroup[],
  sections: GroupSection[],
  mode: GroupMode,
  collapsed: Set<string>,
  expandedGroups: Set<string>,
  folders?: FolderView,
): DisplayItem[] {
  const y = { value: 0 };

  if (mode === "none") {
    const items: DisplayItem[] = [];
    if (folders?.project) {
      emitFolderedGroups(groups, folders.project, "", folders, expandedGroups, items, y);
      return items;
    }
    for (const g of groups) {
      emitGroupItems(g, "", expandedGroups, items, y);
    }
    return items;
  }

  const items: DisplayItem[] = [];
  for (const section of sections) {
    items.push({
      id: `header:${section.label}`,
      type: "header",
      label: section.label,
      count: section.groups.length,
      sectionGroups: section.groups,
      height: HEADER_HEIGHT,
      top: y.value,
    });
    y.value += HEADER_HEIGHT;

    if (!collapsed.has(section.label)) {
      if (mode === "project" && folders) {
        emitFolderedGroups(
          section.groups,
          section.label,
          section.label,
          folders,
          expandedGroups,
          items,
          y,
        );
        continue;
      }
      for (const g of section.groups) {
        emitGroupItems(g, section.label, expandedGroups, items, y);
      }
    }
  }
  return items;
}

/** Placement of a session group's rows in a folder-aware list. */
interface FolderPlace {
  inset?: number;
  dropFolderId: number | null;
  dropProject: string;
}

function emitPlacedGroupItems(
  g: SessionGroup,
  label: string,
  expandedGroups: Set<string>,
  items: DisplayItem[],
  y: { value: number },
  place: FolderPlace,
): void {
  const start = items.length;
  emitGroupItems(g, label, expandedGroups, items, y);
  for (let i = start; i < items.length; i++) Object.assign(items[i]!, place);
}

function emitFolderedGroups(
  groups: SessionGroup[],
  project: string,
  label: string,
  view: FolderView,
  expandedGroups: Set<string>,
  items: DisplayItem[],
  y: { value: number },
): void {
  const projectFolders = view.foldersByProject.get(project) ?? [];
  const byFolder = new Map<number, SessionGroup[]>(projectFolders.map((f) => [f.id, []]));
  const unfiled: SessionGroup[] = [];
  for (const g of groups) {
    const folderId = view.groupFolder.get(g.key);
    const bucket = folderId === undefined ? undefined : byFolder.get(folderId);
    if (bucket) bucket.push(g);
    else unfiled.push(g);
  }

  let lastFolderShown = false;
  for (const folder of projectFolders) {
    const folderGroups = byFolder.get(folder.id)!;
    lastFolderShown = view.expanded.has(folder.id) && folderGroups.length > 0;
    items.push({
      id: `folder:${folder.id}`,
      type: "folder",
      label: folder.name,
      count: folderGroups.length,
      folder,
      sectionGroups: folderGroups,
      dropFolderId: folder.id,
      dropProject: project,
      height: FOLDER_HEADER_HEIGHT,
      top: y.value,
    });
    y.value += FOLDER_HEADER_HEIGHT;
    if (!view.expanded.has(folder.id)) continue;
    for (const g of folderGroups) {
      emitPlacedGroupItems(g, label, expandedGroups, items, y, {
        inset: 1,
        dropFolderId: folder.id,
        dropProject: project,
      });
    }
  }

  // Without the gap, the open last folder's indented rows run straight
  // into the unfiled rows. It belongs to the top level, so a drop on it
  // takes a session out of its folder.
  if (lastFolderShown && unfiled.length > 0) {
    items.push({
      id: `folder-gap:${project}`,
      type: "gap",
      label,
      count: 0,
      dropFolderId: null,
      dropProject: project,
      height: FOLDER_GAP_HEIGHT,
      top: y.value,
    });
    y.value += FOLDER_GAP_HEIGHT;
  }

  for (const g of unfiled) {
    emitPlacedGroupItems(g, label, expandedGroups, items, y, {
      dropFolderId: null,
      dropProject: project,
    });
  }
}

function displaySessionId(item: DisplayItem): string | null {
  if (item.type !== "session") return null;
  if (item.isChild) return item.session?.id ?? null;
  return item.group?.primarySessionId ?? null;
}

/**
 * Find the next rendered session row. When a collapsed lineage or section
 * hides the active session, treat its visible representative as the current
 * position.
 */
export function adjacentVisibleSessionId(
  displayItems: DisplayItem[],
  activeSessionId: string | null,
  delta: number,
): string | null {
  if (!activeSessionId) return null;

  let current = displayItems.findIndex((item) => displaySessionId(item) === activeSessionId);
  if (current < 0) {
    current = displayItems.findIndex((item) => item.memberSessionIds?.includes(activeSessionId));
  }
  if (current < 0) {
    current = displayItems.findIndex(
      (item) =>
        item.type === "session" &&
        item.group?.sessions.some((session) => session.id === activeSessionId),
    );
  }
  if (current < 0) {
    current = displayItems.findIndex(
      (item) =>
        (item.type === "header" || item.type === "folder") &&
        item.sectionGroups?.some((group) =>
          group.sessions.some((session) => session.id === activeSessionId),
        ),
    );
  }

  if (current < 0) {
    const rows = displayItems.filter((item) => item.type === "session");
    const edge = delta > 0 ? rows[0] : rows[rows.length - 1];
    return edge ? displaySessionId(edge) : null;
  }

  for (let next = current + delta; next >= 0 && next < displayItems.length; next += delta) {
    const id = displaySessionId(displayItems[next]!);
    if (id) return id;
  }
  return null;
}

/**
 * Compute total pixel height of the display items list.
 */
export function computeTotalSize(displayItems: DisplayItem[]): number {
  if (displayItems.length === 0) return 0;
  const last = displayItems[displayItems.length - 1]!;
  return last.top + last.height;
}

/**
 * Binary search for the index of the first visible item given
 * scrollY position.  Accounts for OVERSCAN rows before the
 * viewport.
 */
export function findStart(displayItems: DisplayItem[], scrollY: number): number {
  const target = scrollY - OVERSCAN * ITEM_HEIGHT;
  let lo = 0;
  let hi = displayItems.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (displayItems[mid]!.top + displayItems[mid]!.height <= target) {
      lo = mid + 1;
    } else {
      hi = mid;
    }
  }
  return Math.max(0, lo);
}
