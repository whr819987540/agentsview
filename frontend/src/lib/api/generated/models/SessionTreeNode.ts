/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DbSession } from './DbSession';
export type SessionTreeNode = {
  children: any[] | null;
  depth: number;
  is_active: boolean;
  is_branch_start: boolean;
  is_leaf: boolean;
  session: DbSession;
};

