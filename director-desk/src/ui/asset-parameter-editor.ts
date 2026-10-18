import type { AppContext } from '../app-context.ts';
import type { AssetDefinition } from '../assets/catalog/types.ts';
import type { Entity } from '../model.ts';
import { assetParameters } from '../assets/parameters.ts';
import { escape, options } from './common.ts';

/** Shape controls share the object's property page and the existing field transactions. */
export function createAssetParameterEditor(ctx: AppContext) {
    const content = document.querySelector<HTMLElement>('#inspector-content')!;
    content.addEventListener('click', event => {
        const button=(event.target as HTMLElement).closest<HTMLElement>('[data-reset-asset-parameter]');if(!button)return;
        const key=button.dataset.resetAssetParameter!;
        event.stopPropagation();
        const entity = ctx.current(); if (!entity || entity.locked) return;
        ctx.change(() => {
            if (entity.assetParameters) {
                delete entity.assetParameters[key];
                if (!Object.keys(entity.assetParameters).length) delete entity.assetParameters;
            }
        });
    });
    return {
        render(entity: Entity, definition: AssetDefinition) {
            const schema = definition.parameters!;
            const values = assetParameters(entity), human = definition.family === 'human-v2';
            const available = Object.keys(schema).filter(id => !human || (id !== 'outfitLength' || values.outfit > 0) && (id !== 'outfitThickness' || values.outfit > 0 || values.headwear > 0 || values.backpack > 0));
            const controls = available.map(key=>{
                const field = schema[key], value = values[key], label = field.label + (field.unit ? ` / ${field.unit}` : '');
                const attributes = `data-field="assetParameters.${escape(key)}" aria-label="${escape(label)}"`;
                const control = field.choices ? `<select ${attributes}>${options(Object.entries(field.choices), String(value))}</select>`
                    : `<input type="number" ${attributes} value="${value}" min="${field.min}" max="${field.max}" step="${field.step}"/>`;
                return `<div class="field"><span>${escape(label)}</span><div class="parameter-value-row">${control}<button data-reset-asset-parameter="${escape(key)}" aria-label="恢复${escape(field.label)}预设值" title="恢复预设值 ${field.default}">↺</button></div></div>`;
            }).join('');
            return `<div class="asset-parameter-editor"><div class="shape-parameter-grid">${controls}</div></div>`;
        }
    };
}
