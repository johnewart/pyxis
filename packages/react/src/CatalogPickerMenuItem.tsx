/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

import type { ReactNode } from 'react';
import { Box, Menu } from '@mantine/core';
import {
  CATALOG_PICKER_ACTIVE_BACKGROUND,
  catalogPickerInsetDividerStyle,
  catalogPickerMenuItemStyles,
} from './catalogPickerMenuStyles.js';

export type CatalogPickerMenuItemProps = {
  children: ReactNode;
  onClick?: () => void;
  closeMenuOnClick?: boolean;
  disabled?: boolean;
  fw?: number;
  /** Show inset divider beneath this row (default true). */
  showDivider?: boolean;
  leftSection?: ReactNode;
  paddingLeft?: number;
  /** Keyboard-nav highlight: marks this row as the active cursor position. */
  active?: boolean;
};

/** Menu row with optional inset divider beneath (not full-width). */
export function CatalogPickerMenuItem({
  children,
  onClick,
  closeMenuOnClick = true,
  disabled,
  fw,
  showDivider = true,
  leftSection,
  paddingLeft,
  active = false,
}: CatalogPickerMenuItemProps) {
  return (
    <Box
      component="div"
      style={{ width: '100%' }}
      data-picker-active={active ? 'true' : undefined}
    >
      <Menu.Item
        onClick={onClick}
        closeMenuOnClick={closeMenuOnClick}
        disabled={disabled}
        fw={fw}
        leftSection={leftSection}
        styles={{
          item: {
            ...catalogPickerMenuItemStyles.item,
            ...(paddingLeft != null ? { paddingLeft } : {}),
            ...(active ? { background: CATALOG_PICKER_ACTIVE_BACKGROUND } : {}),
          },
          itemLabel: catalogPickerMenuItemStyles.itemLabel,
          itemSection: catalogPickerMenuItemStyles.itemSection,
        }}
      >
        {children}
      </Menu.Item>
      {showDivider ? <Box style={catalogPickerInsetDividerStyle()} /> : null}
    </Box>
  );
}
