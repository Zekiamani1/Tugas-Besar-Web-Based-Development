"use client";

export interface TabItem<T extends string = string> {
  id: T;
  label: string;
  count?: number;
}

export function Tabs<T extends string>({
  tabs,
  activeTab,
  onChange,
  ariaLabel = "Navigasi tab",
}: {
  tabs: ReadonlyArray<TabItem<T>>;
  activeTab: T;
  onChange: (tabId: T) => void;
  ariaLabel?: string;
}) {
  return (
    <div className="tab-bar" role="tablist" aria-label={ariaLabel}>
      {tabs.map((tab) => {
        const isSelected = tab.id === activeTab;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            aria-selected={isSelected}
            className="tab-button"
            onClick={() => onChange(tab.id)}
          >
            {tab.label}
            {typeof tab.count === "number" ? ` (${tab.count})` : ""}
          </button>
        );
      })}
    </div>
  );
}
