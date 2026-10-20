import { Devices } from "./Devices";
import { Profiles } from "./Profiles";
import { ProfilesAccess } from "./ProfilesAccess";

export function AccessAdmin() {
  return (
    <div className="space-y-8">
      <Profiles />
      <Devices />
      <ProfilesAccess />
    </div>
  );
}
