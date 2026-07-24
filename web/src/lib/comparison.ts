/** Comparison data shared by the spec table and the attack-surface chart. */

export type Level = "good" | "partial" | "bad";

export interface Row {
  property: string;
  host: { level: Level; text: string };
  sandbox: { level: Level; text: string };
  devcontainer: { level: Level; text: string };
  vm: { level: Level; text: string };
}

export const ROWS: readonly Row[] = [
  {
    property: "Host secrets reachable",
    host: { level: "bad", text: "all of them" },
    sandbox: { level: "good", text: "none" },
    devcontainer: { level: "partial", text: "often forwarded" },
    vm: { level: "good", text: "none" },
  },
  {
    property: "Blast radius",
    host: { level: "bad", text: "whole machine" },
    sandbox: { level: "good", text: "one project" },
    devcontainer: { level: "partial", text: "project + config" },
    vm: { level: "good", text: "the guest" },
  },
  {
    property: "Built for agent autonomy",
    host: { level: "bad", text: "no" },
    sandbox: { level: "good", text: "15 agents wrapped" },
    devcontainer: { level: "bad", text: "generic" },
    vm: { level: "bad", text: "generic" },
  },
  {
    property: "Lifetime",
    host: { level: "bad", text: "permanent" },
    sandbox: { level: "good", text: "dies on exit" },
    devcontainer: { level: "partial", text: "long-lived" },
    vm: { level: "partial", text: "snapshot / restore" },
  },
  {
    property: "Time to first run",
    host: { level: "good", text: "none" },
    sandbox: { level: "good", text: "one install" },
    devcontainer: { level: "partial", text: "config + rebuild" },
    vm: { level: "bad", text: "provision a guest" },
  },
  {
    property: "Overhead",
    host: { level: "good", text: "native" },
    sandbox: { level: "good", text: "one container" },
    devcontainer: { level: "good", text: "one container" },
    vm: { level: "bad", text: "a whole OS" },
  },
  {
    property: "Parallel agents, isolated branches",
    host: { level: "bad", text: "manual" },
    sandbox: { level: "good", text: "built in" },
    devcontainer: { level: "bad", text: "manual" },
    vm: { level: "partial", text: "one VM each" },
  },
];

/**
 * Radar dimensions, scored 0–10. Higher is better in every axis, so the
 * enclosed area reads directly as "how well contained and how practical".
 */
export interface RadarPoint {
  axis: string;
  host: number;
  sandbox: number;
  devcontainer: number;
  vm: number;
}

export const RADAR: readonly RadarPoint[] = [
  { axis: "Secret safety", host: 1, sandbox: 9, devcontainer: 5, vm: 9 },
  { axis: "Blast containment", host: 1, sandbox: 9, devcontainer: 6, vm: 10 },
  { axis: "Agent ergonomics", host: 9, sandbox: 9, devcontainer: 4, vm: 3 },
  { axis: "Disposability", host: 1, sandbox: 10, devcontainer: 4, vm: 6 },
  { axis: "Startup speed", host: 10, sandbox: 8, devcontainer: 5, vm: 2 },
  { axis: "Low overhead", host: 10, sandbox: 8, devcontainer: 8, vm: 2 },
];

export interface Series {
  key: "host" | "sandbox" | "devcontainer" | "vm";
  label: string;
  color: string;
}

export const SERIES: readonly Series[] = [
  { key: "sandbox", label: "sandbox-cli", color: "var(--contained)" },
  { key: "host", label: "Bare host", color: "var(--exposed)" },
  { key: "devcontainer", label: "Dev Container", color: "var(--signal)" },
  { key: "vm", label: "Full VM", color: "var(--chart-5)" },
];
