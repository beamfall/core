import { entityPickerStrings as entity } from "./entity-picker";
import { pathPickerStrings as path } from "./path-picker";

// One catalog entry for the search-and-pick and folder pickers.
export const pickerStrings = {
  en: { ...entity.en, ...path.en },
  fr: { ...entity.fr, ...path.fr },
  de: { ...entity.de, ...path.de },
  es: { ...entity.es, ...path.es },
  pt: { ...entity.pt, ...path.pt },
} as const;
