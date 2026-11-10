<script setup lang="ts">
import { useId } from 'vue'

const model = defineModel<number | null>({ required: true })
const name = useId()
</script>

<template>
  <fieldset class="star-picker">
    <legend>{{ $t('review.rating') }}</legend>
    <label v-for="n in 5" :key="n" class="star-option">
      <input type="radio" :name="name" :value="n" :checked="model === n" @change="model = n" />
      <span>{{ n }}★</span>
    </label>
    <label class="star-option">
      <input type="radio" :name="name" :checked="model === null" @change="model = null" />
      <span>{{ $t('review.noRatingOption') }}</span>
    </label>
  </fieldset>
</template>
