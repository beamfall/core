## 1. Resolver

- [x] 1.1 Store reads: holder links of a Media and entity→entity links of an Entity, inside one Library
- [x] 1.2 `identify.Service.ContainerEntitiesForMedia`: ordered container chain from the plugin's container kinds
- [x] 1.3 The app scan identifier forwards the seam

## 2. Scan

- [x] 2.1 `scan.ContainerEntityResolver` returns the ordered list; numbered art to the innermost ordered container, folder art to the outermost
- [x] 2.2 End-to-end scan test with the real identifier: show and season posters land on their Entities
- [x] 2.3 Test: a container folder outside the library root gives nothing
