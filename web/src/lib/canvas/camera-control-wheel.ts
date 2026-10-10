type CameraWheelInput = { deltaY: number; deltaX?: number; deltaMode?: number; ctrlKey?: boolean };
type CameraWheelStep = { consume: boolean; direction: -1 | 0 | 1 };

export function createCameraWheelStepper(): (event: CameraWheelInput, now: number) => CameraWheelStep {
    let accumulated = 0;
    let lastEventAt = -Infinity;
    let lastStepAt = -Infinity;
    let lastStepDirection = 0;

    return (event, now) => {
        if (event.ctrlKey || !Number.isFinite(event.deltaY) || event.deltaY === 0 || Math.abs(event.deltaX ?? 0) > Math.abs(event.deltaY)) {
            return { consume: false, direction: 0 };
        }
        const delta = event.deltaY * (event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? 120 : 1);
        const direction = delta > 0 ? 1 : -1;
        if (now - lastEventAt > 200 || (accumulated !== 0 && Math.sign(accumulated) !== direction)) accumulated = 0;
        lastEventAt = now;
        if (direction === lastStepDirection && now - lastStepAt < 180) {
            accumulated = 0;
            return { consume: true, direction: 0 };
        }
        accumulated += delta;
        if (Math.abs(accumulated) < 24) return { consume: true, direction: 0 };
        accumulated = 0;
        lastStepAt = now;
        lastStepDirection = direction;
        return { consume: true, direction };
    };
}
