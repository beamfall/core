// ENROLL / auth area (ADR-0007 §2–§4): first-contact device enrollment, the
// browser→MANAGER promotion, the reusable Admin-Session gate, and sign-out.
//
// AdminSessionGate is the cross-area export: Console wraps its admin-gated
// screens in it (import { AdminSessionGate } from "@/areas/enroll"), so the
// admin-password unlock flow lives in exactly one place.

export {
  AdminSessionGate,
  type AdminSessionRenderProps,
} from "./AdminSessionGate";
export { EnrollPage } from "./EnrollPage";
export { PromotePage } from "./PromotePage";
export { SignOutButton } from "./SignOutButton";
