# Station radio in the served viewer

Ticket CORE-2772.

Station generation already applies hard year and runtime filters. Extend it to
filter by catalog genre and release decade. The pinned catalog plugin
(v0.1.18) does not declare or import a mood field. Let viewers start a station
from an eligible audio item, person, or container and see a localized
explanation on generated queue entries.

The existing queue replacement confirmation and profile and library visibility
checks apply. Station reasons follow queue entries and persist with them.
