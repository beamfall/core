# Operator Responsibility

Beamfall Core is local-first, self-hosted software. The operator runs the server, supplies the
files, and is the publisher and possessor of the library they expose. Beamfall does not host
content, phone home, or perform IP geolocation.

The operator is responsible for:

- the lawful acquisition and possession of the media in their libraries;
- any age gates, record keeping, or regional obligations that apply to what they expose;
- reverse-proxy, DNS, and public-metadata controls for anything published outside Core's
  authenticated surfaces.

Core's own controls (Profiles, Restrictions, the Reveal contract, Admin Sessions) govern what each
signed-in viewer sees. They are product features, not legal advice.
