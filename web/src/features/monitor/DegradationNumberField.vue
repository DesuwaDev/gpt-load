<script setup lang="ts">
import FormField from '@/components/ui/FormField.vue'

withDefaults(
  defineProps<{
    id: string
    label: string
    modelValue: string
    description?: string
    error?: string
    placeholder?: string
    suffix?: string
    inputmode?: 'numeric' | 'decimal'
    disabled?: boolean
  }>(),
  {
    description: undefined,
    error: undefined,
    placeholder: undefined,
    suffix: undefined,
    inputmode: 'numeric',
    disabled: false,
  },
)
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <FormField
    :id="id"
    class="degradation-number-field"
    :label="label"
    :description="description"
    :error="error"
    size="compact"
  >
    <template #default="{ describedBy, invalid }">
      <span class="degradation-number-field__control">
        <input
          :id="id"
          :value="modelValue"
          :inputmode="inputmode"
          autocomplete="off"
          :spellcheck="false"
          :disabled="disabled"
          :placeholder="placeholder"
          :aria-describedby="describedBy"
          :aria-invalid="invalid || undefined"
          @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
        />
        <span v-if="suffix" class="degradation-number-field__suffix" aria-hidden="true">
          {{ suffix }}
        </span>
      </span>
    </template>
  </FormField>
</template>

<style scoped>
.degradation-number-field__control {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: var(--space-2);
}

.degradation-number-field__control input {
  min-width: 0;
  font-variant-numeric: tabular-nums;
}

.degradation-number-field__suffix {
  flex: 0 0 auto;
  color: var(--color-text-faint);
  font-size: var(--text-label-xs);
}
</style>
