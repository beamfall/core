## ADDED Requirements

### Requirement: The server selects one render node for hardware transcode

On Linux the server SHALL enumerate the DRM render nodes at startup and read each node's PCI
vendor. It SHALL select a node from the stored transcode device when that device names an
enumerated render node path, or names a vendor (`intel`, `amd`, `nvidia`) that one of the nodes
has. Otherwise, or when the stored device matches no node, it SHALL select the first Intel node,
then the first AMD node, then the first node.

#### Scenario: Intel is preferred by default

- **WHEN** the host has an AMD node `renderD128` and an Intel node `renderD129` and no device is stored
- **THEN** the server selects `renderD129`

#### Scenario: A stored render node path is honoured

- **WHEN** the stored transcode device is `/dev/dri/renderD128` and that node exists
- **THEN** the server selects `renderD128`

#### Scenario: A stored vendor name is honoured

- **WHEN** the stored transcode device is `amd` and an AMD node exists
- **THEN** the server selects the first AMD node

#### Scenario: An unmatched hint falls back

- **WHEN** the stored transcode device is `/dev/dri/renderD200` and no such node exists
- **THEN** the server logs the mismatch and selects by the default order

### Requirement: Probe, tone-map and encode share the selected render node

The QSV and VA-API probe encodes, the QSV and VA-API tone-map devices and the QSV and VA-API
encodes SHALL all use the selected render node. The QSV probe and encode SHALL initialise the QSV
device on that node explicitly. The node list and the selection SHALL be reported in the server's
internal hardware report and startup log and SHALL NOT be added to the API.

#### Scenario: QSV encodes on the probed GPU

- **WHEN** the selected node is `/dev/dri/renderD129` and QSV is the verified encoder
- **THEN** both the QSV probe and the QSV encode argv carry `-init_hw_device qsv=bfqsv:hw,child_device=/dev/dri/renderD129`
