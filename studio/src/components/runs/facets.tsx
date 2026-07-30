import {
  CheckCircle2,
  CircleDashed,
  CircleSlash,
  Globe,
  Layers,
  Loader2,
  Lock,
  ShieldAlert,
  ShieldCheck,
  Unplug,
  Users,
  XCircle,
} from "lucide-react";
import { AGENT_SEEDS } from "@/lib/constants";
import type { FacetOption } from "@/components/data-table/faceted-filter";

/**
 * The filter vocabularies.
 *
 * Each list matches the badge that renders the same value, colour and icon
 * included — a filter whose "Passed" looks different from the row's "Passed"
 * makes the reader check whether they are the same thing.
 */

export const OUTCOME_FACETS: FacetOption[] = [
  { label: "Running", value: "running", icon: Loader2, tone: "var(--status-running)" },
  { label: "Passed", value: "passed", icon: CheckCircle2, tone: "var(--status-good)" },
  { label: "Verify failed", value: "verify-failed", icon: ShieldAlert, tone: "var(--status-serious)" },
  { label: "Failed", value: "failed", icon: XCircle, tone: "var(--status-critical)" },
  { label: "Stopped", value: "stopped", icon: CircleSlash, tone: "var(--status-idle)" },
  { label: "Created", value: "created", icon: CircleDashed, tone: "var(--status-idle)" },
];

export const KIND_FACETS: FacetOption[] = [
  { label: "Fleet", value: "fleet", icon: Users },
  { label: "Session", value: "interactive", icon: Layers },
];

export const PROFILE_FACETS: FacetOption[] = [
  { label: "prod", value: "prod", icon: ShieldCheck },
  { label: "dev", value: "dev", icon: Layers },
];

export const EGRESS_FACETS: FacetOption[] = [
  { label: "Allowlist", value: "allowlist", icon: Lock },
  { label: "No network", value: "none", icon: Unplug },
  { label: "Unrestricted", value: "default", icon: Globe },
];

/** Every adapter, plus the plain-`run` case, which carries no agent label. */
export const AGENT_FACETS: FacetOption[] = [
  ...AGENT_SEEDS.map((a) => ({ label: a.label, value: a.name })),
  { label: "Plain run", value: "plain run" },
];
