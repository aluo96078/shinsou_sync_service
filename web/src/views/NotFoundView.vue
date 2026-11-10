<template>
  <section class="empty-state">
    <h1>{{ $t('notFound.title') }}</h1>
    <p>{{ $t('notFound.body') }}</p>
    <RouterLink class="btn btn-primary" to="/">{{ $t('notFound.home') }}</RouterLink>
  </section>
</template>
