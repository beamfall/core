## ADDED Requirements

### Requirement: Derivative buckets never duplicate the source

A derivative bucket whose width is greater than or equal to the pixel width of a decodable JPEG or PNG
source SHALL be served from the original file and SHALL NOT be stored as a separate file. A stored
derivative that meets this condition SHALL be removed when that bucket is next resolved.

#### Scenario: Bucket wider than the source

- **WHEN** a 500px poster is requested at bucket w1280
- **THEN** the original file is served and no `poster.w1280.jpg` exists

#### Scenario: Bucket narrower than the source

- **WHEN** a 500px poster is requested at bucket w320
- **THEN** a scaled `poster.w320.jpg` is generated and served
