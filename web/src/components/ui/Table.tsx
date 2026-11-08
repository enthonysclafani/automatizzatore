import type { HTMLAttributes, ReactNode, TdHTMLAttributes, ThHTMLAttributes } from "react";
import { cx } from "./cx";

export function Table({ children, className, label }: { children: ReactNode; className?: string; label?: string }) {
  return (
    <div className={cx("relative w-full overflow-x-auto", className)}>
      <table className="w-full border-collapse text-left text-[13px]" aria-label={label}>
        {children}
      </table>
    </div>
  );
}

export function THead({ children }: { children: ReactNode }) {
  return (
    <thead className="border-b border-line text-[12px] font-medium text-muted">
      {children}
    </thead>
  );
}

export function Th({ className, children, ...rest }: ThHTMLAttributes<HTMLTableCellElement>) {
  return (
    <th scope="col" className={cx("px-3 py-2 font-medium whitespace-nowrap first:pl-4 last:pr-4", className)} {...rest}>
      {children}
    </th>
  );
}

export function Tr({ className, children, ...rest }: HTMLAttributes<HTMLTableRowElement>) {
  return (
    <tr className={cx("border-b border-line last:border-b-0", className)} {...rest}>
      {children}
    </tr>
  );
}

export function Td({ className, children, ...rest }: TdHTMLAttributes<HTMLTableCellElement>) {
  return (
    <td className={cx("px-3 py-2 align-middle first:pl-4 last:pr-4", className)} {...rest}>
      {children}
    </td>
  );
}

/** Right-aligned numeric cell. */
export function TdNum({ className, children, ...rest }: TdHTMLAttributes<HTMLTableCellElement>) {
  return (
    <td className={cx("tnum px-3 py-2 text-right align-middle whitespace-nowrap first:pl-4 last:pr-4", className)} {...rest}>
      {children}
    </td>
  );
}
