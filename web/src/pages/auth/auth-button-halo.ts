export function createAuthButtonHalo() {
    let active: HTMLElement | null = null;
    const hide = () => {
        if (!active) return;
        delete active.dataset.authHalo;
        active.style.removeProperty("--auth-halo-x");
        active.style.removeProperty("--auth-halo-y");
        active = null;
    };
    return {
        hide,
        move(button: HTMLElement | null, clientX: number, clientY: number) {
            if (!button) return hide();
            const rect = button.getBoundingClientRect();
            const x = clientX - rect.left;
            const y = clientY - rect.top;
            if (x < 0 || y < 0 || x > rect.width || y > rect.height) return hide();
            if (active !== button) hide();
            active = button;
            button.style.setProperty("--auth-halo-x", `${x}px`);
            button.style.setProperty("--auth-halo-y", `${y}px`);
            button.dataset.authHalo = "true";
        },
    };
}
