"use client";

import { MembersOf } from "@/components/org/members";
import { Gate } from "@/components/shell/gate";
import { useCurrentOrg } from "@/components/shell/org-switcher";

/** The members of the current organisation (components/org/members.tsx). */
function Members() {
  return <MembersOf org={useCurrentOrg() ?? ""} />;
}

export default function MembersPage() {
  return (
    <Gate need="gateway">
      <Members />
    </Gate>
  );
}
