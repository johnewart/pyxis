/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import type { CSSProperties } from 'react';

/** Picker menu text size — sm (14px) for comfortable readability. */
export const CATALOG_PICKER_MENU_FONT_SIZE = 'var(--mantine-font-size-sm)';

/** Horizontal inset for picker menu item divider lines (not full-width). */
export const CATALOG_PICKER_ITEM_DIVIDER_INSET = 10;

/** Finder-style selection fill for active parent rows and child panes. */
export const CATALOG_PICKER_ACTIVE_BACKGROUND =
  'var(--mantine-primary-color-light)';

/** Text on active picker rows. */
export const CATALOG_PICKER_ACTIVE_COLOR =
  'var(--mantine-primary-color-light-color)';

/** Fixed height for the column browser and filter-result lists. */
export const CATALOG_PICKER_BROWSER_HEIGHT = 280;

/** Single-column picker dropdown width. */
export const CATALOG_PICKER_DROPDOWN_SINGLE_COLUMN_WIDTH = 280;

/** Two-column picker dropdown width (double the single-column width). */
export const CATALOG_PICKER_DROPDOWN_TWO_COLUMN_WIDTH =
  CATALOG_PICKER_DROPDOWN_SINGLE_COLUMN_WIDTH * 2;

export function catalogPickerBrowserFrameStyle(): CSSProperties {
  return {
    display: 'flex',
    width: '100%',
    height: CATALOG_PICKER_BROWSER_HEIGHT,
    maxHeight: CATALOG_PICKER_BROWSER_HEIGHT,
    overflow: 'hidden',
  };
}

export function catalogPickerColumnScrollStyle(): CSSProperties {
  return {
    flex: 1,
    minHeight: 0,
    overflowY: 'auto',
    overflowX: 'hidden',
    WebkitOverflowScrolling: 'touch',
  };
}

export function catalogPickerSearchResultsScrollStyle(): CSSProperties {
  return {
    maxHeight: CATALOG_PICKER_BROWSER_HEIGHT,
    overflowY: 'auto',
    overflowX: 'hidden',
    WebkitOverflowScrolling: 'touch',
  };
}

/** Sticky footer row pinned to the bottom of a column browser pane (e.g. "Reload models"). */
export function catalogPickerColumnFooterStyle(): CSSProperties {
  return {
    flexShrink: 0,
    borderTop: '1px solid var(--mantine-color-default-border)',
  };
}

export function catalogPickerInsetDividerStyle(): CSSProperties {
  return {
    height: 1,
    marginLeft: CATALOG_PICKER_ITEM_DIVIDER_INSET,
    marginRight: CATALOG_PICKER_ITEM_DIVIDER_INSET,
    backgroundColor: 'var(--mantine-color-default-border)',
    opacity: 0.45,
    flexShrink: 0,
  };
}

export const catalogPickerMenuItemStyles = {
  item: {
    width: '100%',
    paddingTop: 5,
    paddingBottom: 5,
    borderRadius: 0,
    fontSize: CATALOG_PICKER_MENU_FONT_SIZE,
    fontWeight: 500,
  },
  itemLabel: {
    flex: 1,
    minWidth: 0,
  },
  itemSection: {
    marginInlineStart: 8,
    flexShrink: 0,
  },
} as const;

export function catalogPickerMenuItemStylesForState(options: {
  selected?: boolean;
  leafSelected?: boolean;
}) {
  const active = options.selected || options.leafSelected;
  return {
    item: {
      ...catalogPickerMenuItemStyles.item,
      ...(active
        ? {
            backgroundColor: CATALOG_PICKER_ACTIVE_BACKGROUND,
            color: CATALOG_PICKER_ACTIVE_COLOR,
          }
        : {}),
    },
    itemLabel: catalogPickerMenuItemStyles.itemLabel,
    itemSection: catalogPickerMenuItemStyles.itemSection,
  } as const;
}

export function catalogPickerActiveColumnStyle(): CSSProperties {
  return {
    backgroundColor: CATALOG_PICKER_ACTIVE_BACKGROUND,
  };
}

export function catalogPickerActiveColumnHeaderStyle(): CSSProperties {
  return {
    padding: '5px 10px 4px',
    borderBottom: '1px solid var(--mantine-color-default-border)',
    backgroundColor: CATALOG_PICKER_ACTIVE_BACKGROUND,
    color: CATALOG_PICKER_ACTIVE_COLOR,
    flexShrink: 0,
  };
}

export function catalogPickerMenuDropdownStyles(twoColumns = false) {
  const width = twoColumns
    ? CATALOG_PICKER_DROPDOWN_TWO_COLUMN_WIDTH
    : CATALOG_PICKER_DROPDOWN_SINGLE_COLUMN_WIDTH;
  return {
    dropdown: {
      padding: 0,
      overflow: 'hidden' as const,
      fontSize: CATALOG_PICKER_MENU_FONT_SIZE,
      minWidth: width,
      width,
    },
  } as const;
}

/** Mantine Menu defaults that allow typing in the filter TextInput. */
export const catalogPickerMenuBehaviorProps = {
  trapFocus: false,
} as const;

export const catalogPickerMenuLabelStyle = {
  fontSize: CATALOG_PICKER_MENU_FONT_SIZE,
} as const;
