/**
 * Helpers for one project's sidebar folders, which can sit inside each
 * other. Callers pass a single project's folders, sorted the way siblings
 * should list.
 */

/** The fields that place a folder in its project's tree. */
export interface TreeFolder {
  id: number;
  /** The folder that holds this one; absent at the project's top level. */
  parent_id?: number;
}

/**
 * The folder that holds each folder, or null at the top level. A parent
 * missing from the list, or a link that would close a cycle, counts as the
 * top level, so a damaged tree still lists every folder once.
 */
export function folderParents(folders: readonly TreeFolder[]): Map<number, number | null> {
  const declared = new Map<number, number | null>();
  for (const f of folders) declared.set(f.id, f.parent_id ?? null);
  for (const [id, parent] of declared) {
    if (parent !== null && !declared.has(parent)) declared.set(id, null);
  }
  const out = new Map<number, number | null>();
  for (const f of folders) {
    let parent = declared.get(f.id) ?? null;
    const seen = new Set<number>();
    for (let up = parent; up !== null && !seen.has(up); up = declared.get(up) ?? null) {
      if (up === f.id) {
        parent = null;
        break;
      }
      seen.add(up);
    }
    out.set(f.id, parent);
  }
  return out;
}

/** Each folder's subfolders in list order; key null holds the top level. */
export function folderChildren<T extends TreeFolder>(
  folders: readonly T[],
): Map<number | null, T[]> {
  const parents = folderParents(folders);
  const out = new Map<number | null, T[]>();
  for (const f of folders) {
    const parent = parents.get(f.id) ?? null;
    let list = out.get(parent);
    if (!list) {
      list = [];
      out.set(parent, list);
    }
    list.push(f);
  }
  return out;
}

/** The folders in display order, each subfolder after its parent, with
 *  depth 0 at the top level. */
export function flattenFolderTree<T extends TreeFolder>(
  folders: readonly T[],
): { folder: T; depth: number }[] {
  const children = folderChildren(folders);
  const out: { folder: T; depth: number }[] = [];
  const visit = (parent: number | null, depth: number) => {
    for (const folder of children.get(parent) ?? []) {
      out.push({ folder, depth });
      visit(folder.id, depth + 1);
    }
  };
  visit(null, 0);
  return out;
}

/** The IDs of a folder and every folder inside it. */
export function folderSubtreeIds(folders: readonly TreeFolder[], id: number): Set<number> {
  const children = folderChildren(folders);
  const out = new Set<number>();
  const visit = (current: number) => {
    out.add(current);
    for (const child of children.get(current) ?? []) visit(child.id);
  };
  visit(id);
  return out;
}

/** The folders that hold a folder, nearest first. */
export function folderAncestorIds(folders: readonly TreeFolder[], id: number): number[] {
  const parents = folderParents(folders);
  const out: number[] = [];
  for (let up = parents.get(id) ?? null; up !== null; up = parents.get(up) ?? null) {
    out.push(up);
  }
  return out;
}
