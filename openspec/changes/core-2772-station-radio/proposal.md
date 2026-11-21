# Station radio in the served viewer

Ticket CORE-2772.

Station generation already applies hard year and runtime filters. Extend it to
filter by catalog genre, catalog mood when present, and release decade. Let
viewers start a station from an audio item, person, or container and see a
localized explanation on generated queue entries.

The existing queue replacement confirmation and profile and library visibility
checks apply. Station reasons follow queue entries and persist with them.
