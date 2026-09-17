<script setup lang="ts">
import AppSwitch from '@/components/ui/AppSwitch.vue'

withDefaults(
  defineProps<{
    modelValue: boolean
    label: string
    description?: string
    disabled?: boolean
  }>(),
  { description: undefined, disabled: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template>
  <div class="degradation-toggle-row">
    <span class="degradation-toggle-row__text">
      <span class="degradation-toggle-row__label">{{ label }}</span>
      <small v-if="description">{{ description }}</small>
    </span>
    <AppSwitch
      :model-value="modelValue"
      :label="label"
      :disabled="disabled"
      @update:model-value="emit('update:modelValue', $event)"
    />
  </div>
</template>

<style scoped>
.degradation-toggle-row {
  display: grid;
  align-items: center;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: var(--space-3);
}

.degradation-toggle-row__text {
  display: grid;
  min-width: 0;
  gap: 2px;
}

.degradation-toggle-row__label {
  color: var(--color-text);
  font-size: var(--text-sm);
  font-weight: 560;
}

.degradation-toggle-row__text small {
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
  line-height: 1.55;
}
</style>
