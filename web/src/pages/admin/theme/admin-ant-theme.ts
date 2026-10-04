import type { ThemeConfig } from "antd";
import { theme as antdTheme } from "antd";
import { getSkinAntOverrides, normalizeSkinID, normalizeSkinDefinition } from "@/lib/skin-themes";

/** 后台保留独立密度与表面，主操作颜色继承站点皮肤。 */
export function getIsolatedAdminAntTheme(dark: boolean, skinID: unknown = "classic"): ThemeConfig {
    const skin = getSkinAntOverrides(skinID, dark ? "dark" : "light");
    const colors = normalizeSkinDefinition(skinID).tokens[dark ? "dark" : "light"];
    const text = colors.text;
    const textSecondary = colors.textMuted;
    const canvas = colors.adminBackground;
    const surface = colors.adminSurface;
    const surfaceMuted = colors.adminSubtle;
    const border = colors.controlBorder;
    const primary = skin.primary || (dark ? "#f5f5f5" : "#171717");
    const primaryFg = skin.primaryForeground || (dark ? "#171717" : "#ffffff");
    const hover = skin.primaryHover || (dark ? "#ffffff" : "#303030");
    const active = skin.primaryActive || (dark ? "#e5e5e5" : "#404040");
    const danger = colors.danger;
    const success = colors.success;
    const warning = colors.warning;
    const info = colors.info;
    const switchOn = colors.switchChecked;
    const switchOff = colors.switchUnchecked;

    return {
        algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
        cssVar: { key: `admin-console-${normalizeSkinID(skinID)}-${dark ? "dark" : "light"}` },
        token: {
            fontFamily: 'ui-sans-serif, "SF Pro Text", "PingFang SC", "Hiragino Sans GB", "Noto Sans SC", system-ui, sans-serif',
            fontSize: 13,
            fontSizeSM: 12,
            borderRadius: skin.buttonRadius ?? 0,
            borderRadiusLG: skin.overlayRadius ?? 0,
            borderRadiusSM: skin.inputRadius ?? 0,
            controlHeight: 32,
            controlHeightSM: 28,
            controlHeightLG: 36,
            lineWidth: 1,
            controlOutlineWidth: 0,
            colorPrimary: primary,
            colorPrimaryHover: hover,
            colorPrimaryActive: active,
            colorText: text,
            colorTextSecondary: textSecondary,
            colorTextTertiary: textSecondary,
            colorBgBase: canvas,
            colorBgContainer: surface,
            colorBgElevated: surface,
            colorBgLayout: canvas,
            colorBorder: border,
            colorBorderSecondary: border,
            colorSuccess: success,
            colorWarning: warning,
            colorError: danger,
            colorInfo: info,
            colorLink: text,
            colorLinkHover: hover,
            boxShadow: "none",
            boxShadowSecondary: dark ? "0 22px 56px rgba(0, 0, 0, 0.48)" : "0 16px 40px rgba(15, 23, 42, 0.16)",
            motionDurationFast: "0.12s",
            motionDurationMid: "0.12s",
            motionDurationSlow: "0.18s",
        },
        components: {
            Button: {
                borderRadius: skin.buttonRadius ?? 0,
                fontWeight: 550,
                paddingInline: 12,
                paddingInlineSM: 8,
                primaryShadow: "none",
                defaultShadow: "none",
                dangerShadow: "none",
                colorPrimary: primary,
                colorPrimaryHover: hover,
                colorPrimaryActive: active,
                defaultBg: skin.controlSurface || surface,
                defaultColor: skin.text || text,
                defaultBorderColor: skin.controlBorder || border,
                defaultHoverBg: skin.controlHover || surfaceMuted,
                defaultHoverColor: skin.text || text,
                defaultHoverBorderColor: skin.controlFocus || border,
                defaultActiveBg: skin.controlActive || surfaceMuted,
                defaultActiveColor: skin.text || text,
                defaultActiveBorderColor: skin.controlFocus || border,
                primaryColor: primaryFg,
            },
            Input: {
                borderRadius: skin.buttonRadius ?? 0,
                activeShadow: "none",
                paddingInline: 10,
                activeBg: surface,
                hoverBg: surface,
            },
            InputNumber: {
                borderRadius: skin.buttonRadius ?? 0,
                activeShadow: "none",
                activeBg: surface,
                hoverBg: surface,
            },
            Select: {
                borderRadius: skin.buttonRadius ?? 0,
                activeOutlineColor: "transparent",
                optionPadding: "7px 10px",
                optionSelectedBg: surfaceMuted,
                optionActiveBg: surfaceMuted,
            },
            DatePicker: {
                activeShadow: "none",
            },
            Switch: {
                colorPrimary: switchOn,
                colorPrimaryHover: switchOn,
                colorTextQuaternary: switchOff,
                colorTextTertiary: switchOff,
            },
            Table: {
                headerBg: surfaceMuted,
                headerColor: textSecondary,
                headerSplitColor: "transparent",
                borderColor: border,
                rowHoverBg: surfaceMuted,
                cellPaddingBlock: 6,
                cellPaddingBlockMD: 6,
                cellPaddingBlockSM: 4,
                cellPaddingInline: 12,
                cellPaddingInlineSM: 8,
            },
            Tabs: {
                horizontalItemPadding: "8px 0",
                itemColor: textSecondary,
                itemSelectedColor: text,
                inkBarColor: text,
            },
            Drawer: {
                colorBgElevated: surface,
                paddingLG: 16,
            },
            Modal: {
                borderRadiusLG: skin.overlayRadius ?? 0,
                contentBg: surface,
                headerBg: surface,
            },
            Tooltip: {
                borderRadius: skin.buttonRadius ?? 0,
                colorBgSpotlight: colors.overlay,
                colorTextLightSolid: colors.text,
            },
            Dropdown: {
                borderRadiusLG: skin.overlayRadius ?? 0,
                controlItemBgHover: surfaceMuted,
                paddingBlock: 4,
            },
            Form: {
                itemMarginBottom: 14,
            },
            Segmented: {
                itemSelectedBg: surface,
                trackBg: surfaceMuted,
            },
            Card: {
                boxShadow: "none",
                boxShadowTertiary: "none",
            },
            Message: {
                borderRadiusLG: skin.overlayRadius ?? 0,
                contentPadding: "8px 14px",
            },
        },
    };
}
