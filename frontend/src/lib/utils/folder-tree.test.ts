import { describe, expect, it } from "vite-plus/test";
import {
  flattenFolderTree,
  folderAncestorIds,
  folderParents,
  folderSubtreeIds,
  type TreeFolder,
} from "./folder-tree.js";

function f(id: number, parent?: number): TreeFolder {
  return parent === undefined ? { id } : { id, parent_id: parent };
}

describe("folder tree", () => {
  // 1 > 2 > 3, 1 > 4, and 5 at the top level, listed out of tree order.
  const folders = [f(3, 2), f(5), f(1), f(4, 1), f(2, 1)];

  it("lists each subfolder after its parent, siblings in list order", () => {
    expect(flattenFolderTree(folders).map(({ folder, depth }) => [folder.id, depth])).toEqual([
      [5, 0],
      [1, 0],
      [4, 1],
      [2, 1],
      [3, 2],
    ]);
  });

  it("walks ancestors nearest first and collects a subtree", () => {
    expect(folderAncestorIds(folders, 3)).toEqual([2, 1]);
    expect(folderAncestorIds(folders, 5)).toEqual([]);
    expect([...folderSubtreeIds(folders, 1)].sort((a, b) => a - b)).toEqual([1, 2, 3, 4]);
  });

  it("puts a folder whose parent is not listed at the top level", () => {
    expect(folderParents([f(1, 99), f(2, 1)])).toEqual(
      new Map([
        [1, null],
        [2, 1],
      ]),
    );
  });

  it("still lists every folder once when parents form a cycle", () => {
    // 1 and 2 hold each other; 3 sits in 1.
    const damaged = [f(1, 2), f(2, 1), f(3, 1)];

    const flat = flattenFolderTree(damaged).map(({ folder }) => folder.id);

    expect(flat.sort((a, b) => a - b)).toEqual([1, 2, 3]);
    expect(folderAncestorIds(damaged, 3)).toEqual([1]);
  });
});
