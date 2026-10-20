import type { ComponentProps } from "react";
import {
  type CompositeGridNavigator,
  CompositeItem,
  CompositeRoot,
} from "@/components/ui/astryx-primitives";

const treeNavigation: CompositeGridNavigator = ({
  event,
  elementsRef,
  highlightedIndex,
  minIndex,
  maxIndex,
}) => {
  if (event.key === "ArrowDown") return Math.min(highlightedIndex + 1, maxIndex);
  if (event.key === "ArrowUp") return Math.max(highlightedIndex - 1, minIndex);

  const currentLevel = Number(elementsRef.current[highlightedIndex]?.getAttribute("aria-level"));
  if (event.key === "ArrowRight") {
    const nextIndex = Math.min(highlightedIndex + 1, maxIndex);
    const nextLevel = Number(elementsRef.current[nextIndex]?.getAttribute("aria-level"));
    return nextLevel > currentLevel ? nextIndex : maxIndex + 1;
  }
  if (event.key !== "ArrowLeft") return highlightedIndex;

  for (let index = highlightedIndex - 1; index >= minIndex; index -= 1) {
    const candidateLevel = Number(elementsRef.current[index]?.getAttribute("aria-level"));
    if (candidateLevel < currentLevel) return index;
  }
  return minIndex - 1;
};

type TreeRootProps = ComponentProps<"div">;

function TreeRoot({ children, ...props }: TreeRootProps) {
  return (
    <CompositeRoot<Record<string, never>, CompositeRoot.State>
      tag="div"
      orientation="both"
      grid={treeNavigation}
      loopFocus={false}
      enableHomeAndEndKeys
      props={[props, { role: "tree" }]}
    >
      {children}
    </CompositeRoot>
  );
}

type TreeItemProps = ComponentProps<"div">;

function TreeItem({ children, ...props }: TreeItemProps) {
  return (
    <CompositeItem<Record<string, never>, CompositeItem.State>
      tag="div"
      props={[props, { role: "treeitem" }]}
    >
      {children}
    </CompositeItem>
  );
}

export const Tree = {
  Root: TreeRoot,
  Item: TreeItem,
};
