import type * as React from "react";

import { cn } from "@/lib/utils";

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "border-input placeholder:text-muted-foreground hover:border-[var(--border-strong)] focus-visible:border-ring aria-invalid:border-destructive flex field-sizing-content min-h-20 w-full rounded-md border bg-[var(--control-bg)] px-3 py-2 text-base transition-[color,border-color] disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
        className,
      )}
      {...props}
    />
  );
}

export { Textarea };
