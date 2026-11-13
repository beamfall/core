import { Devices } from "./Devices";
import { Profiles } from "./Profiles";
import { ProfilesAccess } from "./ProfilesAccess";
import { SignInSecurity } from "./SignInSecurity";

// Profiles & access: who can use the server and how they sign in.
export function AccessAdmin() {
  return (
    <div className="space-y-8">
      <Profiles />
      <Devices />
      <SignInSecurity />
    </div>
  );
}

// Schedules: parental controls (enforced filters, access schedules, grants).
export function ScheduleAdmin() {
  return <ProfilesAccess />;
}
